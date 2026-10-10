package api

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/libdns/libdns"

	"github.com/thomas-bsn/forgeyard/internal/agent"
	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/dns"
	"github.com/thomas-bsn/forgeyard/internal/dns/dnstest"
)

// fakeAgent is a node connected over the real gRPC stream, driven by the test.
type fakeAgent struct {
	stream agentpb.AgentService_ConnectClient
	nodeID int64
}

// connectFakeAgent creates a node through the API, joins it and opens its stream.
func connectFakeAgent(t *testing.T, ts string, server *Server, admin *http.Client, name string, hello ...func(*agentpb.Hello)) *fakeAgent {
	t.Helper()
	tlsConfig, err := server.ca.ServerTLS()
	if err != nil {
		t.Fatal(err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gs := grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsConfig)))
	agentpb.RegisterAgentServiceServer(gs, server.nodes)
	go gs.Serve(lis)
	t.Cleanup(gs.Stop)
	_, server.agentPort, _ = net.SplitHostPort(lis.Addr().String())

	var created joinCommandResponse
	if code := postJSON(t, admin, ts+"/api/admin/nodes", createNodeRequest{Name: name}, &created); code != http.StatusCreated {
		t.Fatalf("create node: %d", code)
	}
	dir := filepath.Join(t.TempDir(), "agent")
	if _, err := agent.Join(context.Background(), agent.JoinOptions{
		Server: ts, Token: flagValue(t, created.Command, "--token"), // the public address does not resolve in tests
		CAFingerprint: flagValue(t, created.Command, "--ca"), StateDir: dir,
	}); err != nil {
		t.Fatal(err)
	}
	st, err := agent.LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	st.AgentServer = lis.Addr().String()
	conn, err := grpc.NewClient(st.AgentServer, grpc.WithTransportCredentials(credentials.NewTLS(st.TLS)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stream, err := agentpb.NewAgentServiceClient(conn).Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	h := &agentpb.Hello{AgentVersion: "test", Info: &agentpb.NodeInfo{Hostname: name, Cpus: 2, LocalIp: "192.168.1." + strconv.Itoa(10+len(name))}}
	for _, f := range hello {
		f(h)
	}
	stream.Send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_Hello{Hello: h}})
	msg, err := stream.Recv()
	if err != nil || msg.GetWelcome() == nil {
		t.Fatalf("welcome: %v %v", msg, err)
	}
	return &fakeAgent{stream: stream, nodeID: msg.GetWelcome().GetNodeId()}
}

// desired waits for the next desired state.
func (f *fakeAgent) desired(t *testing.T) *agentpb.DesiredState {
	t.Helper()
	for {
		msg, err := f.stream.Recv()
		if err != nil {
			t.Fatal(err)
		}
		if d := msg.GetDesiredState(); d != nil {
			return d
		}
	}
}

func TestAppLifecycle(t *testing.T) {
	ts, server := newTestServerWithHandle(t)
	admin := newClient()
	post(t, admin, ts.URL+"/api/setup", setupRequest{Token: testToken, InstanceName: "F", Username: "boss",
		Password: "a-long-enough-password", PublicURL: "https://forgeyard.example.com"})

	mem := dnstest.New("example.com.")
	server.newDNSProvider = func(string, map[string]string) (dns.Provider, error) { return mem, nil }
	if code := put(t, admin, ts.URL+"/api/admin/settings/domain", putDomainSettings{
		PublicURL: "https://forgeyard.example.com", Mode: "provider", Domain: "example.com", PublicIP: "203.0.113.1",
		Provider: "cloudflare", Credentials: map[string]string{"api_token": "x"},
	}); code != http.StatusOK {
		t.Fatalf("domain settings: %d", code)
	}

	// No node yet: nowhere to deploy.
	if code := postJSON(t, admin, ts.URL+"/api/apps", appInput{Name: "blog", Image: "nginx", Port: 80}, nil); code != http.StatusServiceUnavailable {
		t.Fatalf("create without node: %d", code)
	}

	fa := connectFakeAgent(t, ts.URL, server, admin, "node-a")
	if d := fa.desired(t); len(d.GetApps()) != 0 || d.GetIngress().GetMode() != "traefik" {
		t.Fatalf("initial desired state: %v", d)
	}
	if code := put(t, admin, ts.URL+"/api/admin/nodes/"+itoa(fa.nodeID)+"/ingress", nodeIngressRequest{
		PublicIP: "198.51.100.7", Mode: "proxy", HTTPPort: 8090,
	}); code != http.StatusOK {
		t.Fatalf("node ingress: %d", code)
	}
	if d := fa.desired(t); d.GetIngress().GetMode() != "proxy" || d.GetIngress().GetHttpPort() != 8090 {
		t.Fatalf("desired after ingress change: %v", d.GetIngress())
	}

	// Reserved and invalid names.
	for _, bad := range []appInput{
		{Name: "forgeyard", Image: "nginx", Port: 80},
		{Name: "www", Image: "nginx", Port: 80},
		{Name: "Bad_Name", Image: "nginx", Port: 80},
		{Name: "blog", Image: "nginx", Port: 0},
		{Name: "blog", Image: "nginx", Port: 80, Env: map[string]string{"1BAD": "x"}},
	} {
		if code := postJSON(t, admin, ts.URL+"/api/apps", bad, nil); code < 400 {
			t.Fatalf("%+v accepted: %d", bad, code)
		}
	}

	var app appResponse
	if code := postJSON(t, admin, ts.URL+"/api/apps", appInput{
		Name: "blog", Image: "nginx:1.27", Port: 80, Env: map[string]string{"SECRET": "s3cret"},
	}, &app); code != http.StatusCreated {
		t.Fatalf("create app: %d", code)
	}
	if app.URL != "https://blog.example.com" || app.NodeName != "node-a" {
		t.Fatalf("created app: %+v", app)
	}
	if ip := mem.Lookup("example.com.", "blog", "A"); ip != "198.51.100.7" {
		t.Fatalf("DNS record: %q", ip)
	}
	d := fa.desired(t)
	if len(d.GetApps()) != 1 {
		t.Fatalf("desired apps: %v", d.GetApps())
	}
	spec := d.GetApps()[0]
	if spec.GetHostname() != "blog.example.com" || spec.GetEnv()["SECRET"] != "s3cret" || !spec.GetRunning() || spec.GetMemoryBytes() != 512<<20 {
		t.Fatalf("app spec: %v", spec)
	}
	if stored, _ := server.store.GetApp(context.Background(), app.ID); strings.Contains(stored.EnvSealed, "s3cret") {
		t.Fatal("environment stored in clear")
	}
	if code := postJSON(t, admin, ts.URL+"/api/apps", appInput{Name: "blog", Image: "nginx", Port: 80}, nil); code != http.StatusConflict {
		t.Fatalf("duplicate name: %d", code)
	}

	// Caddy's on-demand TLS check accepts app domains and Forgeyard's own, nothing else.
	for domain, want := range map[string]int{
		"blog.example.com": http.StatusOK, "BLOG.example.com.": http.StatusOK, "forgeyard.example.com": http.StatusOK,
		"nope.example.com": http.StatusNotFound, "blog.other.org": http.StatusNotFound, "x.blog.example.com": http.StatusNotFound,
	} {
		if code := get(t, newClient(), ts.URL+"/api/caddy/ask?domain="+domain, nil); code != want {
			t.Errorf("caddy ask %s: %d, want %d", domain, code, want)
		}
	}

	// The agent's report shows in the API.
	fa.stream.Send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_AppStatuses{AppStatuses: &agentpb.AppStatuses{
		Apps: []*agentpb.AppStatus{{AppId: app.ID, State: "running", StartedAt: time.Now().Unix()}},
	}}})
	deadline := time.Now().Add(5 * time.Second)
	for {
		var list []appResponse
		get(t, admin, ts.URL+"/api/apps", &list)
		if len(list) == 1 && list[0].State == "running" {
			if list[0].Env != nil {
				t.Fatal("list exposes the environment")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("status never reported: %+v", list)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Stop keeps the generation, redeploy bumps it.
	postJSON(t, admin, ts.URL+"/api/apps/"+itoa(app.ID)+"/stop", struct{}{}, nil)
	if s := fa.desired(t).GetApps()[0]; s.GetRunning() || s.GetGeneration() != spec.GetGeneration() {
		t.Fatalf("after stop: %v", s)
	}
	postJSON(t, admin, ts.URL+"/api/apps/"+itoa(app.ID)+"/redeploy", struct{}{}, nil)
	if s := fa.desired(t).GetApps()[0]; !s.GetRunning() || s.GetGeneration() != spec.GetGeneration()+1 {
		t.Fatalf("after redeploy: %v", s)
	}

	// Another user sees nothing and cannot touch the app.
	other := newClient()
	if code := post(t, other, ts.URL+"/api/apps/"+itoa(app.ID)+"/stop", struct{}{}).StatusCode; code != http.StatusUnauthorized {
		t.Fatalf("anonymous stop: %d", code)
	}

	// A node with apps cannot be removed; deleting the app removes its DNS record and container.
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/admin/nodes/"+itoa(fa.nodeID), strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	if resp, err := admin.Do(req); err != nil || resp.StatusCode != http.StatusConflict {
		t.Fatalf("delete node with apps: %v %v", resp.StatusCode, err)
	}
	req, _ = http.NewRequest(http.MethodDelete, ts.URL+"/api/apps/"+itoa(app.ID), strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	if resp, err := admin.Do(req); err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete app: %v", err)
	}
	if len(fa.desired(t).GetApps()) != 0 {
		t.Fatal("app still desired after deletion")
	}
	if ip := mem.Lookup("example.com.", "blog", "A"); ip != "" {
		t.Fatalf("DNS record left: %q", ip)
	}
	var list []appResponse
	get(t, admin, ts.URL+"/api/apps", &list)
	if len(list) != 0 {
		t.Fatalf("apps after deletion: %+v", list)
	}
}

func TestDomainConfiguredAfterApps(t *testing.T) {
	ts, server := newTestServerWithHandle(t)
	admin := newClient()
	post(t, admin, ts.URL+"/api/setup", setupRequest{Token: testToken, InstanceName: "F", Username: "boss",
		Password: "a-long-enough-password", PublicURL: "https://forgeyard.example.com"})
	fa := connectFakeAgent(t, ts.URL, server, admin, "node-a")
	fa.desired(t)

	// Without a domain the app has no hostname.
	var app appResponse
	if code := postJSON(t, admin, ts.URL+"/api/apps", appInput{Name: "whoami", Image: "traefik/whoami", Port: 80}, &app); code != http.StatusCreated {
		t.Fatalf("create app: %d", code)
	}
	if h := fa.desired(t).GetApps()[0].GetHostname(); h != "" || app.URL != "" {
		t.Fatalf("hostname without domain: %q %q", h, app.URL)
	}

	// Configuring the domain afterwards creates the record and routes the new domain.
	mem := dnstest.New("example.com.")
	server.newDNSProvider = func(string, map[string]string) (dns.Provider, error) { return mem, nil }
	if code := put(t, admin, ts.URL+"/api/admin/settings/domain", putDomainSettings{
		PublicURL: "https://forgeyard.example.com", Mode: "provider", Domain: "example.com", PublicIP: "203.0.113.1",
		Provider: "cloudflare", Credentials: map[string]string{"api_token": "x"},
	}); code != http.StatusOK {
		t.Fatalf("domain settings: %d", code)
	}
	if h := fa.desired(t).GetApps()[0].GetHostname(); h != "whoami.example.com" {
		t.Fatalf("hostname after domain: %q", h)
	}
	if ip := mem.Lookup("example.com.", "whoami", "A"); ip != "203.0.113.1" {
		t.Fatalf("record after domain: %q", ip)
	}

	// A name another site already uses in the zone is refused, and its record left untouched.
	mem.SetRecords(context.Background(), "example.com.", []libdns.Record{libdns.Address{Name: "games", IP: netip.MustParseAddr("192.0.2.9")}})
	if code := postJSON(t, admin, ts.URL+"/api/apps", appInput{Name: "games", Image: "nginx", Port: 80}, nil); code != http.StatusConflict {
		t.Fatalf("app over an existing record: %d", code)
	}
	if ip := mem.Lookup("example.com.", "games", "A"); ip != "192.0.2.9" {
		t.Fatalf("existing record changed: %q", ip)
	}
}

// drainUntil reads desired states until one has relays matching ok.
func (f *fakeAgent) drainUntil(t *testing.T, ok func(relays int, hosts, targets []string) bool) {
	t.Helper()
	for i := 0; i < 20; i++ {
		d := f.desired(t)
		var hosts, targets []string
		for _, r := range d.GetRelays() {
			hosts = append(hosts, r.GetHostname())
			targets = append(targets, r.GetTarget())
		}
		if ok(len(d.GetRelays()), hosts, targets) {
			return
		}
	}
	t.Fatal("expected relays never arrived")
}

func TestDockerfileApp(t *testing.T) {
	ts, server := newTestServerWithHandle(t)
	admin := newClient()
	post(t, admin, ts.URL+"/api/setup", setupRequest{Token: testToken, InstanceName: "F", Username: "boss",
		Password: "a-long-enough-password", PublicURL: "https://forgeyard.example.com"})
	fa := connectFakeAgent(t, ts.URL, server, admin, "node-a")
	fa.desired(t)

	if code := postJSON(t, admin, ts.URL+"/api/apps", appInput{Name: "site", Dockerfile: "RUN echo no base", Port: 80}, nil); code != http.StatusBadRequest {
		t.Fatalf("Dockerfile without FROM: %d", code)
	}
	dockerfile := "FROM --platform=linux/amd64 nginx:alpine AS base\r\nRUN echo hello > /usr/share/nginx/html/index.html\r\n"
	var app appResponse
	if code := postJSON(t, admin, ts.URL+"/api/apps", appInput{Name: "site", Image: "ignored", Dockerfile: dockerfile, Port: 80}, &app); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	if !strings.HasPrefix(app.Image, "forgeyard/site:") || !strings.Contains(app.Dockerfile, "RUN echo hello") || strings.Contains(app.Dockerfile, "\r") {
		t.Fatalf("created app: %+v", app)
	}
	if app.Logo.AutoURL != imageLogoURL("nginx:alpine") {
		t.Fatalf("logo from the base image: %q", app.Logo.AutoURL)
	}
	var list []appResponse
	get(t, admin, ts.URL+"/api/apps", &list)
	if len(list) != 1 || list[0].Logo.AutoURL != imageLogoURL("nginx:alpine") || list[0].Dockerfile != "" {
		t.Fatalf("listed app: %+v", list)
	}
	spec := fa.desired(t).GetApps()[0]
	if spec.GetImage() != app.Image || spec.GetDockerfile() != app.Dockerfile {
		t.Fatalf("spec: %v", spec)
	}

	// A new Dockerfile is a new tag; going back to an image drops the Dockerfile.
	var edited appResponse
	if code := putJSON(t, admin, ts.URL+"/api/apps/"+itoa(app.ID), appInput{Dockerfile: "FROM nginx:alpine\nRUN true", Port: 80}, &edited); code != http.StatusOK {
		t.Fatalf("edit: %d", code)
	}
	if edited.Image == app.Image || !strings.HasPrefix(edited.Image, "forgeyard/site:") {
		t.Fatalf("edited image: %q", edited.Image)
	}
	var plain appResponse
	if code := putJSON(t, admin, ts.URL+"/api/apps/"+itoa(app.ID), appInput{Image: "nginx:1.27", Port: 80}, &plain); code != http.StatusOK {
		t.Fatalf("back to an image: %d", code)
	}
	if plain.Image != "nginx:1.27" || plain.Dockerfile != "" {
		t.Fatalf("back to an image: %+v", plain)
	}
}
