package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"

	"github.com/thomas-bsn/forgeyard/internal/dns"
	"github.com/thomas-bsn/forgeyard/internal/dns/dnstest"
)

// Behind one public IP, Forgeyard's machine relays the apps of the other nodes that run Traefik behind
// a proxy, to their local address.
func TestRelays(t *testing.T) {
	ts, server := newTestServerWithHandle(t)
	admin := newClient()
	post(t, admin, ts.URL+"/api/setup", setupRequest{Token: testToken, InstanceName: "F", Username: "boss",
		Password: "a-long-enough-password", PublicURL: "https://forgeyard.example.com"})
	mem := dnstest.New("example.com.")
	server.newDNSProvider = func(string, map[string]string) (dns.Provider, error) { return mem, nil }
	put(t, admin, ts.URL+"/api/admin/settings/domain", putDomainSettings{
		PublicURL: "https://forgeyard.example.com", Mode: "provider", Domain: "example.com", PublicIP: "203.0.113.1",
		Provider: "cloudflare", Credentials: map[string]string{"api_token": "x"},
	})

	front := connectFakeAgent(t, ts.URL, server, admin, "nas")
	front.desired(t)
	if _, err := server.store.DB.ExecContext(context.Background(), "UPDATE nodes SET is_local = 1 WHERE id = ?", front.nodeID); err != nil {
		t.Fatal(err)
	}
	pi := connectFakeAgent(t, ts.URL, server, admin, "pi")
	pi.desired(t)
	put(t, admin, ts.URL+"/api/admin/nodes/"+itoa(pi.nodeID)+"/ingress", nodeIngressRequest{Mode: "proxy", HTTPPort: 8090})
	// Apps go to the node with the fewest: the first on nas, the second on pi.
	var a1, a2 appResponse
	postJSON(t, admin, ts.URL+"/api/apps", appInput{Name: "one", Image: "nginx", Port: 80}, &a1)
	postJSON(t, admin, ts.URL+"/api/apps", appInput{Name: "two", Image: "nginx", Port: 80}, &a2)
	if a2.NodeName != "pi" {
		t.Fatalf("second app on %s", a2.NodeName)
	}

	front.drainUntil(t, func(relays int, hosts []string, targets []string) bool {
		return relays == 1 && hosts[0] == "two.example.com" && targets[0] == "http://192.168.1.12:8090"
	})

	// A node with its own public IP is reached directly: no relay.
	if code := put(t, admin, ts.URL+"/api/admin/nodes/"+itoa(pi.nodeID)+"/ingress", nodeIngressRequest{PublicIP: "198.51.100.9", Mode: "proxy", HTTPPort: 8090}); code != http.StatusOK {
		t.Fatalf("pi ingress: %d", code)
	}
	front.drainUntil(t, func(relays int, _, _ []string) bool { return relays == 0 })
}

func TestNodeChoice(t *testing.T) {
	ts, server := newTestServerWithHandle(t)
	admin := newClient()
	post(t, admin, ts.URL+"/api/setup", setupRequest{Token: testToken, InstanceName: "F", Username: "boss",
		Password: "a-long-enough-password", PublicURL: "https://forgeyard.example.com"})
	a := connectFakeAgent(t, ts.URL, server, admin, "nas")
	a.desired(t)
	b := connectFakeAgent(t, ts.URL, server, admin, "pi")
	b.desired(t)

	var choices []nodeChoice
	get(t, admin, ts.URL+"/api/admin/nodes/choices", &choices)
	if len(choices) != 2 || !choices[0].Recommended || choices[1].Recommended {
		t.Fatalf("choices: %+v", choices)
	}
	// Whoever creates the app picks its node.
	var app appResponse
	if code := postJSON(t, admin, ts.URL+"/api/apps", appInput{Name: "here", Image: "nginx", Port: 80, NodeID: b.nodeID}, &app); code != http.StatusCreated || app.NodeName != "pi" {
		t.Fatalf("chosen node: %d %+v", code, app)
	}
	if code := postJSON(t, admin, ts.URL+"/api/apps", appInput{Name: "nowhere", Image: "nginx", Port: 80, NodeID: 999}, nil); code != http.StatusConflict {
		t.Fatalf("unknown node: %d", code)
	}
}

// Moving an app: it starts on the new node while the old one keeps it, which drops it once the new node
// reports it online.
func TestMoveApp(t *testing.T) {
	ts, server := newTestServerWithHandle(t)
	admin := newClient()
	post(t, admin, ts.URL+"/api/setup", setupRequest{Token: testToken, InstanceName: "F", Username: "boss",
		Password: "a-long-enough-password", PublicURL: "https://forgeyard.example.com"})
	nas := connectFakeAgent(t, ts.URL, server, admin, "nas")
	nas.desired(t)
	pi := connectFakeAgent(t, ts.URL, server, admin, "pi")
	pi.desired(t)

	var app appResponse
	postJSON(t, admin, ts.URL+"/api/apps", appInput{Name: "web", Image: "nginx", Port: 80, NodeID: nas.nodeID}, &app)
	nas.desired(t)

	var moved appResponse
	if code := postJSON(t, admin, ts.URL+"/api/admin/apps/"+itoa(app.ID)+"/move", map[string]int64{"nodeId": pi.nodeID}, &moved); code != http.StatusOK {
		t.Fatalf("move: %d", code)
	}
	if moved.NodeName != "pi" || moved.MovingFrom != nas.nodeID {
		t.Fatalf("moved app: %+v", moved)
	}
	if d := pi.desired(t); len(d.GetApps()) != 1 || d.GetApps()[0].GetLeaving() {
		t.Fatalf("new node: %v", d.GetApps())
	}
	if d := nas.desired(t); len(d.GetApps()) != 1 || !d.GetApps()[0].GetLeaving() {
		t.Fatalf("old node while moving: %v", d.GetApps())
	}
	if code := postJSON(t, admin, ts.URL+"/api/admin/apps/"+itoa(app.ID)+"/move", map[string]int64{"nodeId": nas.nodeID}, nil); code != http.StatusConflict {
		t.Fatalf("second move during a move: %d", code)
	}

	// Online on the new node: the old one drops it.
	pi.stream.Send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_AppStatuses{AppStatuses: &agentpb.AppStatuses{
		Apps: []*agentpb.AppStatus{{AppId: app.ID, State: "running"}},
	}}})
	if d := nas.desired(t); len(d.GetApps()) != 0 {
		t.Fatalf("old node after the move: %v", d.GetApps())
	}
	var after appResponse
	get(t, admin, ts.URL+"/api/apps/"+itoa(app.ID), &after)
	if after.MovingFrom != 0 || after.NodeName != "pi" {
		t.Fatalf("app after the move: %+v", after)
	}

	// Users cannot pick nor move.
	if code := get(t, newClient(), ts.URL+"/api/admin/nodes/choices", nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous choices: %d", code)
	}
}
