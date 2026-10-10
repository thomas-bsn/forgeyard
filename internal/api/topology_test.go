package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/dns"
	"github.com/thomas-bsn/forgeyard/internal/dns/dnstest"
)

func TestTopology(t *testing.T) {
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
	server.lookupHost = func(ctx context.Context, host string) ([]string, error) {
		if host == "grafana.example.com" {
			return []string{"203.0.113.1"}, nil
		}
		return nil, context.DeadlineExceeded
	}
	fa := connectFakeAgent(t, ts.URL, server, admin, "pi")
	fa.desired(t)
	var app appResponse
	if code := postJSON(t, admin, ts.URL+"/api/apps", appInput{Name: "grafana", Image: "grafana/grafana", Port: 80}, &app); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	fa.desired(t)

	// The agent reports the app running, listening on 3000, and how its node is wired.
	fa.stream.Send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_AppStatuses{AppStatuses: &agentpb.AppStatuses{
		Apps: []*agentpb.AppStatus{{AppId: app.ID, State: "running", ListeningPorts: []int32{3000}}},
	}}})
	fa.stream.Send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_Topology{Topology: &agentpb.Topology{
		Networks: []*agentpb.DockerNetwork{{Name: "forgeyard", Driver: "bridge", Subnet: "172.20.0.0/16"}, {Name: "none", Driver: "null"}},
		Containers: []*agentpb.TopoContainer{
			{Id: "t1", Name: "forgeyard-traefik", State: "running", Role: "traefik", Endpoints: []*agentpb.NetworkEndpoint{{Network: "forgeyard", Ipv4: "172.20.0.2"}},
				Published: []*agentpb.PublishedPort{{ContainerPort: 443, HostPort: 443, Protocol: "tcp"}}},
			{Id: "a1", Name: "forgeyard-app-" + itoa(app.ID), State: "running", Role: "app", AppId: app.ID, Listening: []int32{3000},
				Endpoints: []*agentpb.NetworkEndpoint{{Network: "forgeyard", Ipv4: "172.20.0.7", Aliases: []string{"grafana"}}}},
			{Id: "p1", Name: "pihole", State: "running", Role: "external", Endpoints: []*agentpb.NetworkEndpoint{{Network: "forgeyard", Ipv4: "172.20.0.9"}}},
		},
	}}})

	var got appNetworkResponse
	deadline := time.Now().Add(5 * time.Second)
	for {
		got = appNetworkResponse{}
		get(t, admin, ts.URL+"/api/apps/"+itoa(app.ID)+"/network", &got)
		if got.Measured && len(got.Listening) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("topology never arrived: %+v", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
	keys := ""
	for _, s := range got.Steps {
		keys += s.Key + ":" + s.State + " "
	}
	if keys != "visitor:info dns:ok entry:info traefik:ok container:error " {
		t.Fatalf("steps: %s", keys)
	}
	if len(got.Issues) != 1 || got.Issues[0].FixPort != 3000 || len(got.Networks) != 1 || got.Networks[0].IP != "172.20.0.7" || got.Networks[0].Subnet != "172.20.0.0/16" {
		t.Fatalf("network: %+v", got)
	}
	if len(got.Neighbors) != 1 || got.Neighbors[0] != "traefik" || got.Others != 1 {
		t.Fatalf("neighbors: %v, others %d", got.Neighbors, got.Others)
	}

	// The whole infrastructure, for admins: unused default networks left out, the issue on the app.
	var topo topologyResponse
	get(t, admin, ts.URL+"/api/admin/topology", &topo)
	if len(topo.Nodes) != 1 || len(topo.Nodes[0].Networks) != 1 || len(topo.Nodes[0].Containers) != 3 {
		t.Fatalf("topology: %+v", topo)
	}
	for _, c := range topo.Nodes[0].Containers {
		if c.Name == "grafana" && (c.Issue == nil || c.Issue.FixPort != 3000 || c.URL != "https://grafana.example.com") {
			t.Fatalf("app in the topology: %+v", c)
		}
	}
	if code := get(t, newClient(), ts.URL+"/api/admin/topology", nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous topology: %d", code)
	}

	// Testing the address asks it as a visitor would, without following redirects.
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "grafana.example.com" {
			t.Errorf("probe host: %s", r.Host)
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer site.Close()
	target, _ := url.Parse(site.URL)
	server.probeClient = &http.Client{Transport: rewriteHost{target}}
	var probe probeResult
	if code := postJSON(t, admin, ts.URL+"/api/apps/"+itoa(app.ID)+"/network/test", struct{}{}, &probe); code != http.StatusOK || probe.OK || probe.Status != http.StatusBadGateway {
		t.Fatalf("probe: %d %+v", code, probe)
	}
}
