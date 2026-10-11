package api

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/dns"
	"github.com/thomas-bsn/forgeyard/internal/dns/dnstest"
	"github.com/thomas-bsn/forgeyard/internal/tunnel"
)

// A tunneled node's apps are relayed by Forgeyard's machine to the server, which passes each request on
// to the node's Traefik through the node's agent.
func TestTunnel(t *testing.T) {
	ts, server := newTestServerWithHandle(t)
	server.httpPort = "8080"
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
	pc := connectFakeAgent(t, ts.URL, server, admin, "pc")
	pc.desired(t)
	var tunneled nodeResponse
	if code := putJSON(t, admin, ts.URL+"/api/admin/nodes/"+itoa(pc.nodeID)+"/ingress", nodeIngressRequest{Mode: "tunnel", HTTPPort: 8090}, &tunneled); code != http.StatusOK || tunneled.IngressMode != "tunnel" {
		t.Fatalf("tunnel: %d %+v", code, tunneled)
	}
	if d := pc.desired(t); d.GetIngress().GetMode() != "tunnel" {
		t.Fatalf("pc's ingress: %v", d.GetIngress())
	}
	var a1, a2 appResponse
	postJSON(t, admin, ts.URL+"/api/apps", appInput{Name: "one", Image: "nginx", Port: 80}, &a1)
	postJSON(t, admin, ts.URL+"/api/apps", appInput{Name: "two", Image: "nginx", Port: 80}, &a2)
	if a2.NodeName != "pc" {
		t.Fatalf("second app on %s", a2.NodeName)
	}
	// Forgeyard's machine sends the app to the server's HTTP port, on its own local address.
	front.drainUntil(t, func(relays int, hosts, targets []string) bool {
		return relays == 1 && hosts[0] == "two.example.com" && targets[0] == "http://192.168.1.13:8080"
	})
	if ip := mem.Lookup("example.com.", "two", "A"); ip != "203.0.113.1" {
		t.Fatalf("tunneled app's record: %q", ip)
	}

	// The node's Traefik, which the fake agent connects its tunnels to.
	traefik := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost { // echoes a body bigger than a tunnel chunk
			io.Copy(w, r.Body)
			return
		}
		io.WriteString(w, "app at "+r.Host)
	}))
	defer traefik.Close()
	go func() {
		for {
			msg, err := pc.stream.Recv()
			if err != nil {
				return
			}
			if open := msg.GetTunnelOpen(); open != nil {
				go func(id string) {
					stream, err := pc.client.Tunnel(context.Background())
					if err != nil {
						return
					}
					stream.Send(&agentpb.TunnelChunk{TunnelId: id})
					c, err := net.Dial("tcp", traefik.Listener.Addr().String())
					if err != nil {
						return
					}
					tunnel.Pipe(tunnel.New(stream, nil, func() { stream.CloseSend() }), c)
				}(open.GetTunnelId())
			}
		}
	}()

	for i := 0; i < 3; i++ { // the connection is kept and reused
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
		req.Host = "two.example.com"
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || string(body) != "app at two.example.com" {
			t.Fatalf("through the tunnel: %d %q", resp.StatusCode, body)
		}
	}
	big := strings.Repeat("forgeyard ", 50_000)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/upload", strings.NewReader(big))
	req.Host = "two.example.com"
	if resp, err := http.DefaultClient.Do(req); err != nil {
		t.Fatal(err)
	} else if body, _ := io.ReadAll(resp.Body); string(body) != big {
		t.Fatalf("big body: %d bytes back", len(body))
	}
	// Forgeyard's own pages are not tunneled.
	if code := get(t, admin, ts.URL+"/api/health", nil); code != http.StatusOK {
		t.Fatalf("health: %d", code)
	}

	// The last step of adding a node tries both legs.
	target, _ := url.Parse(ts.URL)
	server.probeClient = &http.Client{Transport: rewriteHost{target}}
	pc.stream.Send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_Topology{Topology: &agentpb.Topology{
		Containers: []*agentpb.TopoContainer{{Id: "t", Name: "forgeyard-traefik", Role: "traefik", State: "running"}},
	}}})
	for deadline := time.Now().Add(5 * time.Second); ; {
		var check nodeCheck
		postJSON(t, admin, ts.URL+"/api/admin/nodes/"+itoa(pc.nodeID)+"/check", struct{}{}, &check)
		if check.Traefik == "ok" && check.Reach == "ok" && check.ReachTarget == "tunnel" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("check: %+v", check)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
