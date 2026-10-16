package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/tunnel"
)

// SFTP goes through the gateway to the app's container: here a fake agent serving a folder.
func TestSFTPGateway(t *testing.T) {
	ts, server := newTestServerWithHandle(t)
	admin := newClient()
	post(t, admin, ts.URL+"/api/setup", setupRequest{Token: testToken, InstanceName: "F", Username: "boss",
		Password: "a-long-enough-password", PublicURL: "https://forgeyard.example.com"})
	fa := connectFakeAgent(t, ts.URL, server, admin, "node-a")
	fa.desired(t)

	hostKey, err := LoadSSHHostKey(filepath.Join(t.TempDir(), "host_key"))
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go server.ServeSSH(ctx, l, hostKey)
	server.sshPort = 2222

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sshPub, _ := ssh.NewPublicKey(pub)
	if code := postJSON(t, admin, ts.URL+"/api/me/ssh-keys", map[string]string{"publicKey": string(ssh.MarshalAuthorizedKey(sshPub))}, nil); code != http.StatusCreated {
		t.Fatalf("add key: %d", code)
	}
	var app appResponse
	if code := postJSON(t, admin, ts.URL+"/api/apps", appInput{Name: "site", Image: "nginx", Port: 80}, &app); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	if app.SFTP != "sftp -P 2222 site@forgeyard.example.com" {
		t.Fatalf("sftp command: %q", app.SFTP)
	}
	fa.desired(t)
	appID := app.ID
	fa.stream.Send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_AppStatuses{AppStatuses: &agentpb.AppStatuses{
		Apps: []*agentpb.AppStatus{{AppId: appID, State: "running"}},
	}}})

	files := t.TempDir()
	var mu sync.Mutex
	var sftpFail string // when set, the node cannot start SFTP and says so
	go func() {
		for {
			msg, err := fa.stream.Recv()
			if err != nil {
				return
			}
			if open := msg.GetTunnelOpen(); open != nil && open.GetSftpAppId() == appID {
				go func(id string) {
					stream, err := fa.client.Tunnel(context.Background())
					if err != nil {
						return
					}
					mu.Lock()
					fail := sftpFail
					mu.Unlock()
					if fail != "" {
						stream.Send(&agentpb.TunnelChunk{TunnelId: id, Eof: true, Data: []byte(fail)})
						stream.CloseSend()
						stream.Recv()
						return
					}
					stream.Send(&agentpb.TunnelChunk{TunnelId: id})
					conn := tunnel.New(stream, nil, func() { stream.CloseSend() })
					srv, _ := sftp.NewServer(conn, sftp.WithServerWorkingDirectory(files))
					srv.Serve()
					conn.Close()
				}(open.GetTunnelId())
			}
		}
	}()

	signer, _ := ssh.NewSignerFromKey(priv)
	client, err := ssh.Dial("tcp", l.Addr().String(), &ssh.ClientConfig{
		User: "site", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.FixedHostKey(hostKey.PublicKey()), Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var sc *sftp.Client
	for deadline := time.Now().Add(5 * time.Second); ; { // until the app's state reached the server
		if sc, err = sftp.NewClient(client); err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	big := bytes.Repeat([]byte("forgeyard "), 100_000) // well over a tunnel chunk
	f, err := sc.Create("data.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Write(big)
	f.Close()
	f, err = sc.Open("data.txt")
	if err != nil {
		t.Fatal(err)
	}
	back, _ := io.ReadAll(f)
	f.Close()
	sc.Close()
	if !bytes.Equal(back, big) {
		t.Fatalf("read back %d bytes", len(back))
	}
	if b, _ := os.ReadFile(filepath.Join(files, "data.txt")); !bytes.Equal(b, big) {
		t.Fatalf("on the node: %d bytes", len(b))
	}

	// A container where SFTP cannot start: the client hears why.
	mu.Lock()
	sftpFail = "copie du serveur SFTP dans le conteneur : lecture seule"
	mu.Unlock()
	sess, _ := client.NewSession()
	stderr, _ := sess.StderrPipe()
	if err := sess.RequestSubsystem("sftp"); err != nil {
		t.Fatal(err)
	}
	if msg, _ := io.ReadAll(stderr); !strings.Contains(string(msg), "lecture seule") {
		t.Fatalf("failed sftp: %q", msg)
	}
}
