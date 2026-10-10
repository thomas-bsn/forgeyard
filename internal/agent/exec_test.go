package agent

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/docker"
)

// Needs a Docker daemon: FORGEYARD_DOCKER_TEST=1 go test ./internal/agent/
func TestFindShellAgainstDocker(t *testing.T) {
	if os.Getenv("FORGEYARD_DOCKER_TEST") == "" {
		t.Skip("set FORGEYARD_DOCKER_TEST=1 to run against a real Docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dc, err := docker.New(docker.DefaultHost())
	if err != nil {
		t.Fatal(err)
	}
	for image, want := range map[string][]string{
		"alpine:3.21":          {"/bin/sh", "-l"},
		"debian:bookworm-slim": {"/bin/bash", "-l"},
		"traefik/whoami":       nil,
	} {
		if err := dc.Pull(ctx, image); err != nil {
			t.Fatal(err)
		}
		name := "forgeyard-shell-test"
		dc.Remove(ctx, name)
		if err := dc.Create(ctx, name, map[string]any{"Image": image}); err != nil {
			t.Fatal(err)
		}
		got, err := findShell(ctx, dc, name)
		dc.Remove(context.Background(), name)
		if !slices.Equal(got, want) || (want == nil) != (err != nil) {
			t.Errorf("%s: %v, %v", image, got, err)
		}
	}
}
