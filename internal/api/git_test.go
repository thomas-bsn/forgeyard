package api

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/tunnel"
)

func TestGitCommand(t *testing.T) {
	for cmd, want := range map[string][2]string{
		"git-receive-pack '/site.git'": {"receive-pack", "/site.git"},
		"git-upload-pack 'site.git'":   {"upload-pack", "site.git"},
		"git receive-pack 'site'":      {"receive-pack", "site"},
	} {
		svc, path, ok := gitCommand(cmd)
		if !ok || svc != want[0] || path != want[1] {
			t.Errorf("%q: %q %q %v", cmd, svc, path, ok)
		}
	}
	for _, cmd := range []string{"ls", "git-receive-packx '/a'", "git status"} {
		if _, _, ok := gitCommand(cmd); ok {
			t.Errorf("%q taken for git", cmd)
		}
	}
	for b, ok := range map[string]bool{"main": true, "feature/x": true, "v1.2": true, "-x": false, "a..b": false, "a b": false, "x.lock": false, "": false} {
		if validGitBranch(b) != ok {
			t.Errorf("branch %q: %v", b, !ok)
		}
	}
}

// An app deployed by git push: the real git client pushes through the gateway, the node builds the
// pushed files (here a fake agent reading them), and the push shows the build until the app is online.
// SFTP goes through the same gateway to the app's container.
func TestGitPushAndSFTP(t *testing.T) {
	for _, bin := range []string{"git", "ssh"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip(bin + " is not installed")
		}
	}
	ts, server := newTestServerWithHandle(t)
	server.repoRoot = t.TempDir()
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
	server.sshHost = "127.0.0.1"
	server.sshPort = l.Addr().(*net.TCPAddr).Port

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sshPub, _ := ssh.NewPublicKey(pub)
	if code := postJSON(t, admin, ts.URL+"/api/me/ssh-keys", map[string]string{"publicKey": string(ssh.MarshalAuthorizedKey(sshPub))}, nil); code != http.StatusCreated {
		t.Fatalf("add key: %d", code)
	}

	// Created without a port: the Dockerfile's EXPOSE gives it at the first push. Nothing runs before.
	var app appResponse
	if code := postJSON(t, admin, ts.URL+"/api/apps", appInput{Name: "site", GitBranch: "main"}, &app); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	remote := "ssh://site@127.0.0.1:" + strconv.Itoa(server.sshPort) + "/site.git"
	if app.State != "awaiting-push" || app.GitBranch != "main" || app.GitRemote != remote || app.Port != 0 {
		t.Fatalf("app: %+v", app)
	}
	if d := fa.desired(t); len(d.GetApps()) != 0 {
		t.Fatalf("desired before a push: %v", d)
	}

	appID := app.ID

	// The fake node: builds what it is sent, then reports the app running; serves SFTP from a folder.
	files := t.TempDir()
	var mu sync.Mutex
	var built []string         // the files of each build
	var sftpFail string        // when set, the node cannot start SFTP and says so
	var startedAt atomic.Int64 // when the last build's container started
	go func() {
		for {
			msg, err := fa.stream.Recv()
			if err != nil {
				return
			}
			if d := msg.GetDesiredState(); d != nil {
				for _, a := range d.GetApps() {
					if a.GetGitCommit() != "" {
						go fakeBuild(t, fa, a, &mu, &built, &startedAt)
					}
				}
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
	go func() { // statuses, every few hundred milliseconds, like an agent
		for ctx.Err() == nil {
			st := &agentpb.AppStatus{AppId: appID, State: "running", StartedAt: startedAt.Load()}
			if st.StartedAt == 0 {
				st.State = "stopped"
			}
			fa.stream.Send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_AppStatuses{AppStatuses: &agentpb.AppStatuses{Apps: []*agentpb.AppStatus{st}}}})
			time.Sleep(200 * time.Millisecond)
		}
	}()

	// The developer's repository, and git talking to the gateway with their key.
	home := t.TempDir()
	keyFile := filepath.Join(home, "id_ed25519")
	block, _ := ssh.MarshalPrivateKey(priv, "")
	os.WriteFile(keyFile, pem.EncodeToMemory(block), 0o600)
	work := filepath.Join(home, "work")
	env := append(os.Environ(), "HOME="+home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_SSH_COMMAND=ssh -i "+keyFile+" -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR")
	git := func(dir string, args ...string) (string, error) {
		cmd := exec.Command("git", append([]string{"-c", "user.name=Dev", "-c", "user.email=dev@example.com", "-c", "init.defaultBranch=main"}, args...)...)
		cmd.Dir, cmd.Env = dir, env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	mustGit := func(dir string, args ...string) string {
		t.Helper()
		out, err := git(dir, args...)
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return out
	}
	os.MkdirAll(work, 0o755)
	mustGit(work, "init", "-q")
	mustGit(work, "remote", "add", "forgeyard", remote)
	os.WriteFile(filepath.Join(work, "index.html"), []byte("bonjour"), 0o644)
	mustGit(work, "add", ".")
	mustGit(work, "commit", "-qm", "Sans Dockerfile")

	// No Dockerfile: the commit is kept, nothing is deployed.
	out, _ := git(work, "push", "forgeyard", "main")
	if !strings.Contains(out, "pas de Dockerfile") {
		t.Fatalf("push without Dockerfile: %s", out)
	}

	os.WriteFile(filepath.Join(work, "Dockerfile"), []byte("FROM nginx:alpine\nCOPY index.html /usr/share/nginx/html/\nEXPOSE 8080/tcp\n"), 0o644)
	mustGit(work, "add", ".")
	mustGit(work, "commit", "-qm", "Ajoute le Dockerfile")
	commit := strings.TrimSpace(mustGit(work, "rev-parse", "HEAD"))
	out = mustGit(work, "push", "forgeyard", "main")
	for _, want := range []string{"port 8080, lu dans l'EXPOSE", commit[:7] + " « Ajoute le Dockerfile »", "Step 1/3 : FROM nginx:alpine", "en ligne"} {
		if !strings.Contains(out, want) {
			t.Fatalf("push: %q missing in\n%s", want, out)
		}
	}
	mu.Lock()
	if len(built) != 1 || built[0] != "Dockerfile,index.html" {
		t.Fatalf("built: %v", built)
	}
	mu.Unlock()
	get(t, admin, ts.URL+"/api/apps/"+itoa(app.ID), &app)
	if app.GitCommit != commit || app.Port != 8080 || app.Image != "forgeyard/site:"+commit[:12] || app.State != "running" {
		t.Fatalf("pushed app: %+v", app)
	}

	// Another branch is kept, not deployed; the same commit again deploys nothing.
	mustGit(work, "checkout", "-qb", "dev")
	out = mustGit(work, "push", "forgeyard", "dev")
	if !strings.Contains(out, "l'app tourne déjà le dernier commit de la branche « main »") {
		t.Fatalf("push to dev: %s", out)
	}

	// The repository clones back.
	clone := filepath.Join(home, "clone")
	mustGit(home, "clone", "-q", remote, clone)
	if b, err := os.ReadFile(filepath.Join(clone, "Dockerfile")); err != nil || !strings.Contains(string(b), "EXPOSE 8080") {
		t.Fatalf("clone: %q %v", b, err)
	}

	// A path that is not the app's is refused.
	if out, err := git(home, "clone", "-q", strings.Replace(remote, "/site.git", "/other.git", 1), filepath.Join(home, "other")); err == nil || !strings.Contains(out, "ce dépôt est celui de l'app « site »") {
		t.Fatalf("other path: %v %s", err, out)
	}

	// SFTP: files written and read back in the app's container.
	signer, _ := ssh.NewSignerFromKey(priv)
	client, err := ssh.Dial("tcp", l.Addr().String(), &ssh.ClientConfig{
		User: "site", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.FixedHostKey(hostKey.PublicKey()), Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	sc, err := sftp.NewClient(client)
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

// fakeBuild builds an app like an agent: it gets the files from the server, sends some output and the
// result, then starts the container.
func fakeBuild(t *testing.T, fa *fakeAgent, a *agentpb.AppSpec, mu *sync.Mutex, built *[]string, startedAt *atomic.Int64) {
	stream, err := fa.client.Tunnel(context.Background())
	if err != nil {
		t.Error(err)
		return
	}
	stream.Send(&agentpb.TunnelChunk{TunnelId: "source/" + itoa(a.GetId()) + "/" + a.GetGitCommit()})
	var archive bytes.Buffer
	for {
		chunk, err := stream.Recv()
		if err != nil {
			t.Errorf("source: %v", err)
			return
		}
		archive.Write(chunk.GetData())
		if chunk.GetEof() {
			break
		}
	}
	var names []string
	tr := tar.NewReader(&archive)
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		if h.Typeflag != tar.TypeXGlobalHeader { // git archive's commit ID
			names = append(names, h.Name)
		}
	}
	mu.Lock()
	*built = append(*built, strings.Join(names, ","))
	mu.Unlock()
	stream.Send(&agentpb.TunnelChunk{Data: []byte("Step 1/3 : FROM nginx:alpine\n")})
	stream.Send(&agentpb.TunnelChunk{Eof: true})
	stream.CloseSend()
	stream.Recv()
	time.Sleep(1100 * time.Millisecond) // a container starts after its build, in the next second
	startedAt.Store(time.Now().Unix())
}
