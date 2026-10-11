package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
)

func TestAddNodeInNetwork(t *testing.T) {
	ts, server := newTestServerWithHandle(t)
	admin := newClient()
	post(t, admin, ts.URL+"/api/setup", setupRequest{Token: testToken, InstanceName: "F", Username: "boss",
		Password: "a-long-enough-password", PublicURL: "https://forgeyard.example.com"})
	nas := connectFakeAgent(t, ts.URL, server, admin, "nas")
	nas.desired(t)
	if _, err := server.store.DB.ExecContext(context.Background(), "UPDATE nodes SET is_local = 1, local_ip = '192.168.1.39' WHERE id = ?", nas.nodeID); err != nil {
		t.Fatal(err)
	}

	// Bad settings leave no node behind.
	if code := postJSON(t, admin, ts.URL+"/api/admin/nodes", createNodeRequest{Name: "pi", LAN: true, Ingress: "proxy", HTTPPort: 8080}, nil); code != http.StatusBadRequest {
		t.Fatalf("port taken by Forgeyard accepted: %d", code)
	}
	var created joinCommandResponse
	if code := postJSON(t, admin, ts.URL+"/api/admin/nodes", createNodeRequest{Name: "pi", LAN: true, Ingress: "relay", HTTPPort: 8090}, &created); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	if created.Node.IngressMode != "relay" || !created.Node.LAN || !strings.Contains(created.DockerCommand, "--agent-server 192.168.1.39:") || !strings.HasPrefix(created.AgentServer, "192.168.1.39:") {
		t.Fatalf("created: %+v\n%s", created.Node, created.DockerCommand)
	}

	// The last step: connected, Traefik up, reachable over the local network.
	site := httptest.NewServer(http.NotFoundHandler()) // Traefik answers 404 to an unknown host
	defer site.Close()
	target, _ := url.Parse(site.URL)
	server.probeClient = &http.Client{Transport: rewriteHost{target}}
	pi := connectFakeAgent(t, ts.URL, server, admin, "pi2")
	pi.desired(t)
	server.store.DB.ExecContext(context.Background(), "UPDATE nodes SET relayed = 1, ingress_mode = 'proxy', lan = 1, local_ip = '192.168.1.50' WHERE id = ?", pi.nodeID)
	// A just connected agent is still starting Traefik: neither it nor the path is an error yet.
	pi.stream.Send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_Topology{Topology: &agentpb.Topology{}}})
	for deadline := time.Now().Add(5 * time.Second); ; {
		var check nodeCheck
		postJSON(t, admin, ts.URL+"/api/admin/nodes/"+itoa(pi.nodeID)+"/check", struct{}{}, &check)
		if check.Connected && check.Traefik == "unknown" && check.Reach == "skip" && check.ReachTarget == "" {
			break
		}
		if check.Traefik == "down" || check.Reach == "error" || time.Now().After(deadline) {
			t.Fatalf("check while Traefik starts: %+v", check)
		}
		time.Sleep(20 * time.Millisecond)
	}
	pi.stream.Send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_Topology{Topology: &agentpb.Topology{
		Containers: []*agentpb.TopoContainer{{Id: "t", Name: "forgeyard-traefik", Role: "traefik", State: "running"}},
	}}})
	deadline := time.Now().Add(5 * time.Second)
	for {
		var check nodeCheck
		postJSON(t, admin, ts.URL+"/api/admin/nodes/"+itoa(pi.nodeID)+"/check", struct{}{}, &check)
		if check.Connected && check.Traefik == "ok" && check.Reach == "ok" && check.ReachTarget == "192.168.1.50:8090" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("check: %+v", check)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
