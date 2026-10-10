package api

import (
	"context"
	"net/http"
	"testing"

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
