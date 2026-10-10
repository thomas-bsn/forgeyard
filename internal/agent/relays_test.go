package agent

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/docker"
)

func TestRelaysConfig(t *testing.T) {
	got := string(relaysConfig([]*agentpb.Relay{{Hostname: "app.example.com", Target: "http://192.168.1.20:8090"}}, "proxy"))
	for _, want := range []string{"relay-app-example-com:", "rule: \"Host(`app.example.com`)\"", "entryPoints: [web]", "url: \"http://192.168.1.20:8090\"", "passHostHeader: true"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if got := string(relaysConfig(nil, "proxy")); strings.Contains(got, "http:") {
		t.Errorf("empty relays: %q", got)
	}
}

// Needs a Docker daemon: FORGEYARD_DOCKER_TEST=1 go test ./internal/agent/ -run RelaysAgainstDocker
func TestRelaysAgainstDocker(t *testing.T) {
	if os.Getenv("FORGEYARD_DOCKER_TEST") == "" {
		t.Skip("set FORGEYARD_DOCKER_TEST=1 to run against a real Docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	dc, err := docker.New(docker.DefaultHost())
	if err != nil {
		t.Fatal(err)
	}
	const target = "forgeyard-relay-test-target"
	dc.Remove(ctx, target)
	if err := dc.EnsureNetwork(ctx, networkName); err != nil {
		t.Fatal(err)
	}
	if err := dc.Pull(ctx, "traefik/whoami"); err != nil {
		t.Fatal(err)
	}
	if err := dc.Create(ctx, target, map[string]any{"Image": "traefik/whoami", "HostConfig": map[string]any{"NetworkMode": networkName}}); err != nil {
		t.Fatal(err)
	}
	defer dc.Remove(context.Background(), target)
	dc.Start(ctx, target)

	r := NewReconciler(dc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer dc.Remove(context.Background(), traefikName)
	ingress := &agentpb.Ingress{Mode: "proxy", HttpPort: 18095}
	r.SetDesired(&agentpb.DesiredState{Ingress: ingress, Relays: []*agentpb.Relay{{Hostname: "relay.test", Target: "http://" + target + ":80"}}})
	r.reconcile(ctx)

	deadline := time.Now().Add(30 * time.Second)
	for {
		req, _ := http.NewRequest(http.MethodGet, "http://localhost:18095/", nil)
		req.Host = "relay.test"
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 && strings.Contains(string(body), "Host: relay.test") {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("relay never answered: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Changing the relays updates the file without recreating Traefik.
	r.SetDesired(&agentpb.DesiredState{Ingress: ingress, Relays: []*agentpb.Relay{{Hostname: "other.test", Target: "http://" + target + ":80"}}})
	r.reconcile(ctx)
	deadline = time.Now().Add(30 * time.Second)
	for {
		req, _ := http.NewRequest(http.MethodGet, "http://localhost:18095/", nil)
		req.Host = "other.test"
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("updated relay never answered")
		}
		time.Sleep(500 * time.Millisecond)
	}
}
