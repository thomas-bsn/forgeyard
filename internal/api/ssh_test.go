package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
)

func TestSSHGateway(t *testing.T) {
	ts, server := newTestServerWithHandle(t)
	admin := newClient()
	post(t, admin, ts.URL+"/api/setup", setupRequest{Token: testToken, InstanceName: "F", Username: "boss",
		Password: "a-long-enough-password", PublicURL: "https://forgeyard.example.com"})
	fa := connectFakeAgent(t, ts.URL, server, admin, "node-a")
	fa.desired(t)

	// A sandbox: no port, no address, a plain Linux image, and the gateway's command.
	server.sshPort = 2222
	var box appResponse
	if code := postJSON(t, admin, ts.URL+"/api/apps", appInput{Name: "box", Kind: "sandbox"}, &box); code != http.StatusCreated {
		t.Fatalf("create sandbox: %d", code)
	}
	if box.Kind != "sandbox" || box.URL != "" || box.Image != defaultSandboxImage || box.Port != 0 || box.SSH != "ssh box@forgeyard.example.com -p 2222" {
		t.Fatalf("sandbox: %+v", box)
	}
	if spec := fa.desired(t).GetApps()[0]; !spec.GetSandbox() || spec.GetHostname() != "" {
		t.Fatalf("sandbox spec: %v", spec)
	}

	// Keys: bad ones refused, a good one stored once.
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sshPub, _ := ssh.NewPublicKey(pub)
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " me@laptop"
	if code := postJSON(t, admin, ts.URL+"/api/me/ssh-keys", map[string]string{"publicKey": "not a key"}, nil); code != http.StatusBadRequest {
		t.Fatalf("bad key: %d", code)
	}
	var key sshKeyResponse
	if code := postJSON(t, admin, ts.URL+"/api/me/ssh-keys", map[string]string{"publicKey": line}, &key); code != http.StatusCreated || key.Name != "me@laptop" || key.Type != "ssh-ed25519" {
		t.Fatalf("add key: %d %+v", code, key)
	}
	if code := postJSON(t, admin, ts.URL+"/api/me/ssh-keys", map[string]string{"publicKey": line}, nil); code != http.StatusConflict {
		t.Fatalf("same key twice: %d", code)
	}

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

	signer, _ := ssh.NewSignerFromKey(priv)
	dial := func(user string, s ssh.Signer) (*ssh.Client, error) {
		return ssh.Dial("tcp", l.Addr().String(), &ssh.ClientConfig{
			User: user, Auth: []ssh.AuthMethod{ssh.PublicKeys(s)},
			HostKeyCallback: ssh.FixedHostKey(hostKey.PublicKey()), Timeout: 5 * time.Second,
		})
	}
	if _, err := dial("nope", signer); err == nil {
		t.Fatal("unknown app accepted")
	}
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	other, _ := ssh.NewSignerFromKey(otherPriv)
	if _, err := dial("box", other); err == nil {
		t.Fatal("unknown key accepted")
	}

	// Not running yet: the session explains it.
	client, err := dial("box", signer)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	sess, _ := client.NewSession()
	out, err := sess.CombinedOutput("uname")
	var exit *ssh.ExitError
	if !errors.As(err, &exit) || exit.ExitStatus() != 1 || !strings.Contains(string(out), "pas en ligne") {
		t.Fatalf("stopped sandbox: %q %v", out, err)
	}

	// Running: the command goes to the agent, its output and exit code come back.
	fa.stream.Send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_AppStatuses{AppStatuses: &agentpb.AppStatuses{
		Apps: []*agentpb.AppStatus{{AppId: box.ID, State: "running"}},
	}}})
	go func() {
		for {
			msg, err := fa.stream.Recv()
			if err != nil {
				return
			}
			if st := msg.GetExecStart(); st != nil {
				if st.GetAppId() != box.ID || st.GetCommand() != "uname" {
					t.Errorf("exec start: %v", st)
				}
				for _, o := range []*agentpb.ExecOutput{{Data: []byte("Linux\r\n")}, {Closed: true, ExitCode: 3}} {
					o.SessionId = st.GetSessionId()
					fa.stream.Send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_ExecOutput{ExecOutput: o}})
				}
			}
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		sess, _ := client.NewSession()
		out, err = sess.CombinedOutput("uname")
		if !strings.Contains(string(out), "pas en ligne") || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !errors.As(err, &exit) || exit.ExitStatus() != 3 || string(out) != "Linux\r\n" {
		t.Fatalf("command: %q %v", out, err)
	}
	var keys []sshKeyResponse
	get(t, admin, ts.URL+"/api/me/ssh-keys", &keys)
	if len(keys) != 1 || keys[0].LastUsedAt == 0 {
		t.Fatalf("keys: %+v", keys)
	}
}
