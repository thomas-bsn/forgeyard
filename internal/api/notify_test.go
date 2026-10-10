package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
)

// fakeWebhooks receives the webhook posts the server makes, whatever host they target.
type fakeWebhooks struct {
	mu    sync.Mutex
	posts []map[string]any
	paths []string
}

func (f *fakeWebhooks) install(t *testing.T, server *Server) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.posts = append(f.posts, body)
		f.paths = append(f.paths, r.URL.Path)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(ts.Close)
	target, _ := url.Parse(ts.URL)
	server.httpClient = &http.Client{Transport: rewriteHost{target}}
}

type rewriteHost struct{ to *url.URL }

func (rw rewriteHost) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = rw.to.Scheme, rw.to.Host
	return http.DefaultTransport.RoundTrip(r)
}

func (f *fakeWebhooks) waitTitle(t *testing.T, path, contains string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		for i, p := range f.posts {
			embeds, _ := p["embeds"].([]any)
			if len(embeds) > 0 && f.paths[i] == path && strings.Contains(embeds[0].(map[string]any)["title"].(string), contains) {
				f.mu.Unlock()
				return
			}
		}
		f.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no notification %q to %s; got %v", contains, path, f.posts)
}

func TestCrashSuspensionAndNotifications(t *testing.T) {
	ts, server := newTestServerWithHandle(t)
	admin := newClient()
	post(t, admin, ts.URL+"/api/setup", setupRequest{Token: testToken, InstanceName: "F", Username: "boss",
		Password: "a-long-enough-password", PublicURL: "https://forgeyard.example.com"})
	hooks := &fakeWebhooks{}
	hooks.install(t, server)

	if code := putJSON(t, admin, ts.URL+"/api/admin/settings/notifications", notifySettings{Webhook: "https://example.com/hook"}, nil); code != http.StatusBadRequest {
		t.Fatalf("non-Discord webhook accepted: %d", code)
	}
	adminHook := "https://discord.com/api/webhooks/1/admin"
	if code := putJSON(t, admin, ts.URL+"/api/admin/settings/notifications", notifySettings{Webhook: adminHook, Events: map[string]bool{"crashes": true}}, nil); code != http.StatusOK {
		t.Fatalf("admin webhook: %d", code)
	}
	if code := putJSON(t, admin, ts.URL+"/api/me/notifications", map[string]string{"webhook": "https://discord.com/api/webhooks/2/me"}, nil); code != http.StatusOK {
		t.Fatalf("own webhook: %d", code)
	}
	if code := postJSON(t, admin, ts.URL+"/api/me/notifications/test", struct{}{}, nil); code != http.StatusNoContent {
		t.Fatalf("test: %d", code)
	}
	hooks.waitTitle(t, "/api/webhooks/2/me", "Notifications")

	fa := connectFakeAgent(t, ts.URL, server, admin, "node-a")
	fa.desired(t)
	var app appResponse
	postJSON(t, admin, ts.URL+"/api/apps", appInput{Name: "flaky", Image: "nginx", Port: 80}, &app)
	fa.desired(t)

	// Docker restarts it: once, then twice more between two reports.
	for _, restarts := range []int32{0, 1, 3} {
		fa.stream.Send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_AppStatuses{AppStatuses: &agentpb.AppStatuses{
			Apps: []*agentpb.AppStatus{{AppId: app.ID, State: "running", RestartCount: restarts, ExitCode: 1}},
		}}})
		time.Sleep(50 * time.Millisecond)
	}
	if s := fa.desired(t).GetApps()[0]; s.GetRunning() {
		t.Fatal("crashing app still running")
	}
	hooks.waitTitle(t, "/api/webhooks/2/me", "suspendue")
	hooks.waitTitle(t, "/api/webhooks/1/admin", "suspendue")
	var got appResponse
	get(t, admin, ts.URL+"/api/apps/"+itoa(app.ID), &got)
	if !got.CrashSuspended || got.Running {
		t.Fatalf("app after crashes: %+v", got)
	}

	// Starting it again lifts the suspension.
	var started appResponse
	postJSON(t, admin, ts.URL+"/api/apps/"+itoa(app.ID)+"/start", struct{}{}, &started)
	if started.CrashSuspended || !started.Running {
		t.Fatalf("app after start: %+v", started)
	}
}
