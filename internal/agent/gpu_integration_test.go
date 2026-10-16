//go:build integration

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/docker"
)

// TestProbeGpus asks the local Docker for NVIDIA GPUs, the way the agent does. Without a GPU (the usual
// test machine) the check must say so, not fail; with one, it must list it. It needs Docker:
//
//	go test -tags integration -run TestProbeGpus ./internal/agent/
func TestProbeGpus(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	dc, err := docker.New(docker.DefaultHost())
	if err != nil {
		t.Fatal(err)
	}
	res := probeGpus(ctx, dc)
	t.Logf("status=%s error=%q gpus=%v", res.GetStatus(), res.GetError(), res.GetGpus())
	switch res.GetStatus() {
	case "unsupported":
		if res.GetError() == "" {
			t.Fatal("no reason given")
		}
	case "ok":
	default:
		t.Fatalf("the check failed: %s", res.GetError())
	}
	if _, err := dc.Inspect(ctx, gpuProbeName); err == nil {
		t.Fatal("the probe container was left behind")
	}
}
