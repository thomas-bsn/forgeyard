package agent

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/pkg/sftp"

	"github.com/thomas-bsn/forgeyard/internal/docker"
)

// Needs a Docker daemon: FORGEYARD_DOCKER_TEST=1 go test -run TestSFTPAgainstDocker ./internal/agent/
// The helper goes into an image without any shell nor tools, and serves its files.
func TestSFTPAgainstDocker(t *testing.T) {
	if os.Getenv("FORGEYARD_DOCKER_TEST") == "" {
		t.Skip("set FORGEYARD_DOCKER_TEST=1 to run against a real Docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	dc, err := docker.New(docker.DefaultHost())
	if err != nil {
		t.Fatal(err)
	}
	info, err := dc.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	arch := map[string]string{"x86_64": "amd64", "aarch64": "arm64"}[info.Architecture]
	if arch == "" {
		arch = runtime.GOARCH
	}
	helper := filepath.Join(t.TempDir(), "forgeyard-agent")
	build := exec.Command("go", "build", "-o", helper, "../../cmd/agent")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+arch)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the helper: %v\n%s", err, out)
	}
	t.Setenv("FORGEYARD_SFTP_HELPER", helper)

	const appID = 990001
	name := containerName(appID)
	dc.Remove(ctx, name)
	if err := dc.Pull(ctx, "traefik/whoami"); err != nil {
		t.Fatal(err)
	}
	if err := dc.Create(ctx, name, map[string]any{"Image": "traefik/whoami"}); err != nil {
		t.Fatal(err)
	}
	defer dc.Remove(context.Background(), name)
	if err := dc.Start(ctx, name); err != nil {
		t.Fatal(err)
	}

	for range 2 { // the second session finds the helper there
		path, err := installHelper(ctx, dc, name)
		if err != nil {
			t.Fatal(err)
		}
		p, err := dc.ExecPipe(ctx, name, []string{path, "sftp-server"})
		if err != nil {
			t.Fatal(err)
		}
		c, err := sftp.NewClientPipe(p, p)
		if err != nil {
			t.Fatalf("sftp client: %v (%s)", err, p.Stderr())
		}
		data := bytes.Repeat([]byte("forgeyard "), 50_000)
		if err := c.MkdirAll("/tmp"); err != nil { // scratch: no /tmp
			t.Fatal(err)
		}
		f, err := c.Create("/tmp/data.txt")
		if err != nil {
			t.Fatal(err)
		}
		f.Write(data)
		f.Close()
		f, err = c.Open("/tmp/data.txt")
		if err != nil {
			t.Fatal(err)
		}
		back, _ := io.ReadAll(f)
		f.Close()
		if !bytes.Equal(back, data) {
			t.Fatalf("read back %d bytes", len(back))
		}
		if _, err := c.Stat("/whoami"); err != nil {
			t.Fatalf("the image's own files: %v", err)
		}
		c.Close()
		p.Close()
	}
}
