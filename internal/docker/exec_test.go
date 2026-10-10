package docker

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// Needs a Docker daemon and an image: FORGEYARD_DOCKER_TEST=1 go test ./internal/docker/
func TestExecAgainstDocker(t *testing.T) {
	if os.Getenv("FORGEYARD_DOCKER_TEST") == "" {
		t.Skip("set FORGEYARD_DOCKER_TEST=1 to run against a real Docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c, err := New(DefaultHost())
	if err != nil {
		t.Fatal(err)
	}
	const name = "forgeyard-exec-test"
	c.Remove(ctx, name)
	if err := c.Pull(ctx, "alpine:3.21"); err != nil {
		t.Fatal(err)
	}
	if err := c.Create(ctx, name, map[string]any{"Image": "alpine:3.21", "Cmd": []string{"sleep", "60"}}); err != nil {
		t.Fatal(err)
	}
	defer c.Remove(context.Background(), name)
	if err := c.Start(ctx, name); err != nil {
		t.Fatal(err)
	}
	e, err := c.StartExec(ctx, name, []string{"sh"}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Resize(ctx, 120, 40); err != nil {
		t.Fatal(err)
	}
	io.WriteString(e, "stty size; echo hello-$((40+2)); exit 3\n")
	var out bytes.Buffer
	io.Copy(&out, e)
	e.Close()
	if !strings.Contains(out.String(), "hello-42") || !strings.Contains(out.String(), "40 120") {
		t.Fatalf("output: %q", out.String())
	}
	if code, err := e.ExitCode(ctx); err != nil || code != 3 {
		t.Fatalf("exit code %d, %v", code, err)
	}
}
