package agent

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/docker"
)

// Needs a Docker daemon: FORGEYARD_DOCKER_TEST=1 go test ./internal/agent/
func TestTopologyAgainstDocker(t *testing.T) {
	if os.Getenv("FORGEYARD_DOCKER_TEST") == "" {
		t.Skip("set FORGEYARD_DOCKER_TEST=1 to run against a real Docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	dc, err := docker.New(docker.DefaultHost())
	if err != nil {
		t.Fatal(err)
	}
	const net, name = "forgeyard-topo-test", "forgeyard-topo-test-web"
	dc.Remove(ctx, name)
	if err := dc.EnsureNetwork(ctx, net); err != nil {
		t.Fatal(err)
	}
	defer dc.RemoveNetwork(context.Background(), net)
	if err := dc.Pull(ctx, "traefik/whoami"); err != nil {
		t.Fatal(err)
	}
	if err := dc.Create(ctx, name, map[string]any{
		"Image": "traefik/whoami", "ExposedPorts": map[string]any{"80/tcp": map[string]any{}},
		"HostConfig":       map[string]any{"NetworkMode": net, "PortBindings": map[string]any{"80/tcp": []map[string]string{{"HostPort": ""}}}},
		"NetworkingConfig": map[string]any{"EndpointsConfig": map[string]any{net: map[string]any{"Aliases": []string{"web"}}}},
	}); err != nil {
		t.Fatal(err)
	}
	defer dc.Remove(context.Background(), name)
	if err := dc.Start(ctx, name); err != nil {
		t.Fatal(err)
	}

	topo, err := topology(ctx, dc)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(topo.GetNetworks(), func(n *agentpb.DockerNetwork) bool { return n.GetName() == net }) {
		t.Fatalf("network missing: %v", topo.GetNetworks())
	}
	for _, c := range topo.GetContainers() {
		if c.GetName() != name {
			continue
		}
		if c.GetRole() != "external" || len(c.GetEndpoints()) != 1 || c.GetEndpoints()[0].GetNetwork() != net ||
			c.GetEndpoints()[0].GetIpv4() == "" || !slices.Contains(c.GetEndpoints()[0].GetAliases(), "web") {
			t.Fatalf("container: %v", c)
		}
		if len(c.GetPublished()) != 1 || c.GetPublished()[0].GetContainerPort() != 80 || c.GetPublished()[0].GetHostPort() == 0 {
			t.Fatalf("published: %v", c.GetPublished())
		}
		return
	}
	t.Fatal("container missing")
}
