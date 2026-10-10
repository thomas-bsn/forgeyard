package docker

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Needs a Docker daemon: FORGEYARD_DOCKER_TEST=1 go test ./internal/docker/
func TestBuildAgainstDocker(t *testing.T) {
	if os.Getenv("FORGEYARD_DOCKER_TEST") == "" {
		t.Skip("set FORGEYARD_DOCKER_TEST=1 to run against a real Docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c, err := New(DefaultHost())
	if err != nil {
		t.Fatal(err)
	}
	const tag = "forgeyard-build-test:1"
	removeImage := func() {
		if resp, err := c.do(context.Background(), http.MethodDelete, "/images/"+tag+"?force=1", nil); err == nil {
			resp.Body.Close()
		}
	}
	removeImage()
	defer removeImage()

	if err := c.Build(ctx, tag, "FROM alpine:3.21\nRUN echo built > /built\nCMD [\"cat\", \"/built\"]\n"); err != nil {
		t.Fatal(err)
	}
	if err := c.call(ctx, http.MethodGet, "/images/"+tag+"/json", nil, nil); err != nil {
		t.Fatalf("built image missing: %v", err)
	}

	err = c.Build(ctx, tag, "FROM alpine:3.21\nRUN echo about to fail && exit 3\n")
	if err == nil || !strings.Contains(err.Error(), "about to fail") {
		t.Fatalf("failed build: %v", err)
	}
}
