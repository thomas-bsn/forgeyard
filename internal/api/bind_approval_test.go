package api

import (
	"context"
	"errors"
	"github.com/libdns/libdns"
	"github.com/thomas-bsn/forgeyard/internal/dns"
	"github.com/thomas-bsn/forgeyard/internal/dns/dnstest"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
)

func answerBindChecks(node *fakeAgent, checks func(*agentpb.ValidateBindMounts) *agentpb.BindValidationResult) <-chan *agentpb.DesiredState {
	states := make(chan *agentpb.DesiredState, 32)
	go func() {
		defer close(states)
		for {
			msg, err := node.stream.Recv()
			if err != nil {
				return
			}
			if check := msg.GetValidateBindMounts(); check != nil {
				res := checks(check)
				res.Id = check.Id
				_ = node.stream.Send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_BindValidationResult{BindValidationResult: res}})
			}
			if state := msg.GetDesiredState(); state != nil {
				states <- state
			}
		}
	}()
	return states
}

func TestBindApprovalCreatesExactConfigurationOnce(t *testing.T) {
	base, server, superadmin, _ := setupStackServer(t)
	node := connectFakeAgent(t, base, server, superadmin, "media-node", func(h *agentpb.Hello) { h.BindMounts = true })
	node.desired(t)
	states := answerBindChecks(node, func(check *agentpb.ValidateBindMounts) *agentpb.BindValidationResult {
		return &agentpb.BindValidationResult{Mounts: check.Mounts}
	})
	owner := bindTestUser(t, server, base, "lea", "user")
	staff := bindTestUser(t, server, base, "ordinary-admin", "admin")
	input := map[string]any{"name": "media-app", "image": "nginx:alpine", "port": 8080, "memoryMb": 256, "nodeId": node.nodeID, "env": map[string]string{"TOKEN": "keep-secret"}, "bindMounts": []map[string]any{{"source": "/srv/media", "target": "/media"}}}
	var row bindRequestResponse
	if code := postJSON(t, owner, base+"/api/bind-requests", input, &row); code != http.StatusCreated {
		t.Fatalf("submit: %d", code)
	}
	approve := base + "/api/admin/bind-requests/" + itoa(row.ID) + "/approve"
	for _, client := range []*http.Client{owner, staff} {
		if code := postJSON(t, client, approve, struct{}{}, nil); code != http.StatusForbidden {
			t.Fatalf("unauthorized approval: %d", code)
		}
	}
	if code := postJSON(t, superadmin, approve, struct{}{}, &row); code != http.StatusOK || row.Status != "created" || row.AppID == 0 {
		t.Fatalf("approve: %d %+v", code, row)
	}
	select {
	case desired := <-states:
		if len(desired.Apps) != 1 {
			t.Fatalf("desired: %v", desired)
		}
		app := desired.Apps[0]
		if app.GetName() != "media-app" || app.GetEnv()["TOKEN"] != "keep-secret" || app.GetPort() != 8080 || app.GetMemoryBytes() != 256<<20 || !app.GetRunning() || !app.GetBindAuthorized() {
			t.Fatalf("desired config: %v", app)
		}
		if len(app.GetBindMounts()) != 1 || !app.BindMounts[0].ReadOnly || app.BindMounts[0].Source != "/srv/media" || app.BindMounts[0].Target != "/media" {
			t.Fatalf("desired mounts: %v", app)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no deployment after approval")
	}
	var app appResponse
	if code := get(t, owner, base+"/api/apps/"+itoa(row.AppID), &app); code != http.StatusOK || app.OwnerName != "lea" || app.NodeID != node.nodeID || app.Env["TOKEN"] != "keep-secret" {
		t.Fatalf("created app: %d %+v", code, app)
	}
	if code := postJSON(t, superadmin, approve, struct{}{}, &row); code != http.StatusOK {
		t.Fatalf("repeat approval: %d", code)
	}
	var apps []appResponse
	get(t, superadmin, base+"/api/apps", &apps)
	if len(apps) != 1 {
		t.Fatalf("duplicate apps: %+v", apps)
	}
	if code := postJSON(t, superadmin, base+"/api/admin/apps/"+itoa(app.ID)+"/move", map[string]any{"nodeId": 1, "leaveData": true}, nil); code != http.StatusConflict {
		t.Fatalf("move bypass: %d", code)
	}
	// An ordinary configuration change preserves approved mounts and cannot hide them under a named volume.
	modified := map[string]any{"image": "nginx:alpine", "port": 8081, "memoryMb": 256, "env": map[string]string{"TOKEN": "changed"}, "volumes": []string{"/media"}}
	if code := putJSON(t, owner, base+"/api/apps/"+itoa(app.ID), modified, nil); code != http.StatusConflict {
		t.Fatalf("mount shadow: %d", code)
	}
	modified["volumes"] = []string{}
	if code := putJSON(t, owner, base+"/api/apps/"+itoa(app.ID), modified, &app); code != http.StatusOK || len(app.BindMounts) != 1 {
		t.Fatalf("ordinary config: %d %+v", code, app)
	}

	// Reconnecting with an older agent cannot silently drop the new fields and run without controls.
	legacy, err := node.client.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.CloseSend()
	if err = legacy.Send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_Hello{Hello: &agentpb.Hello{AgentVersion: "old", Info: &agentpb.NodeInfo{Hostname: "media-node"}}}}); err != nil {
		t.Fatal(err)
	}
	if first, err := legacy.Recv(); err != nil || first.GetWelcome() == nil {
		t.Fatalf("legacy welcome: %v %v", first, err)
	}
	next, err := legacy.Recv()
	if err != nil || next.GetDesiredState() == nil || len(next.GetDesiredState().Apps) != 1 || next.GetDesiredState().Apps[0].Running {
		t.Fatalf("legacy agent must stop bind app: %v %v", next, err)
	}
}

func TestSuperadminBindSubmissionAutoApprovesAndReportsFailure(t *testing.T) {
	base, server, admin, _ := setupStackServer(t)
	node := connectFakeAgent(t, base, server, admin, "bind-node", func(h *agentpb.Hello) { h.BindMounts = true })
	node.desired(t)
	states := answerBindChecks(node, func(check *agentpb.ValidateBindMounts) *agentpb.BindValidationResult {
		if check.Mounts[0].Source == "/srv/missing" {
			return &agentpb.BindValidationResult{Error: "source absente"}
		}
		return &agentpb.BindValidationResult{Mounts: check.Mounts}
	})
	input := map[string]any{"name": "auto-media", "image": "nginx:alpine", "port": 80, "nodeId": node.nodeID, "bindMounts": []map[string]any{{"source": "/srv/media", "target": "/media", "readOnly": false}}}
	var row bindRequestResponse
	if code := postJSON(t, admin, base+"/api/bind-requests", input, &row); code != http.StatusCreated || row.Status != "created" || row.DecidedAt == 0 || row.AppID == 0 {
		t.Fatalf("auto approval: %d %+v", code, row)
	}
	<-states
	row = bindRequestResponse{}
	input["name"] = "missing-media"
	input["bindMounts"] = []map[string]any{{"source": "/srv/missing", "target": "/media"}}
	if code := postJSON(t, admin, base+"/api/bind-requests", input, &row); code != http.StatusCreated || row.Status != "failed" || row.Reason != "source absente" || row.AppID != 0 {
		t.Fatalf("failed auto approval: %d %+v", code, row)
	}
	if code := postJSON(t, admin, base+"/api/admin/bind-requests/"+itoa(row.ID)+"/retry", struct{}{}, nil); code != http.StatusConflict {
		t.Fatalf("retry missing source: %d", code)
	}
	var apps []appResponse
	get(t, admin, base+"/api/apps", &apps)
	if len(apps) != 1 {
		t.Fatalf("failed validation deployed: %+v", apps)
	}
}

func TestBindApprovalConcurrentDecisionCreatesOnce(t *testing.T) {
	base, server, admin, _ := setupStackServer(t)
	owner := bindTestUser(t, server, base, "author", "user")
	node := connectFakeAgent(t, base, server, admin, "bind-concurrent", func(h *agentpb.Hello) { h.BindMounts = true })
	node.desired(t)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	states := answerBindChecks(node, func(check *agentpb.ValidateBindMounts) *agentpb.BindValidationResult {
		entered <- struct{}{}
		<-release
		return &agentpb.BindValidationResult{Mounts: check.Mounts}
	})
	input := map[string]any{"name": "once", "image": "nginx:alpine", "port": 80, "nodeId": node.nodeID, "bindMounts": []map[string]any{{"source": "/srv/media", "target": "/media"}}}
	var row bindRequestResponse
	postJSON(t, owner, base+"/api/bind-requests", input, &row)
	url := base + "/api/admin/bind-requests/" + itoa(row.ID) + "/approve"
	done := make(chan int, 1)
	go func() {
		resp, err := admin.Post(url, "application/json", strings.NewReader("{}"))
		if err != nil {
			done <- 0
			return
		}
		resp.Body.Close()
		done <- resp.StatusCode
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("approval did not request validation")
	}
	if code := postJSON(t, admin, url, struct{}{}, nil); code != http.StatusConflict {
		t.Fatalf("concurrent approval: %d", code)
	}
	close(release)
	if code := <-done; code != http.StatusOK {
		t.Fatalf("first approval: %d", code)
	}
	<-states
	var apps []appResponse
	get(t, admin, base+"/api/apps", &apps)
	if len(apps) != 1 {
		t.Fatalf("duplicated resource: %+v", apps)
	}
}

type failingBindDNS struct {
	dns.Provider
	fail atomic.Bool
}

func (p *failingBindDNS) SetRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	if p.fail.Load() {
		return nil, errors.New("temporary DNS failure")
	}
	return p.Provider.SetRecords(ctx, zone, records)
}
func TestBindApprovalRetriesSameResourceAfterPartialDNSFailure(t *testing.T) {
	base, server, admin, _ := setupStackServer(t)
	provider := &failingBindDNS{Provider: dnstest.New("example.com.")}
	provider.fail.Store(true)
	server.newDNSProvider = func(string, map[string]string) (dns.Provider, error) { return provider, nil }
	node := connectFakeAgent(t, base, server, admin, "bind-retry", func(h *agentpb.Hello) { h.BindMounts = true })
	node.desired(t)
	states := answerBindChecks(node, func(check *agentpb.ValidateBindMounts) *agentpb.BindValidationResult {
		return &agentpb.BindValidationResult{Mounts: check.Mounts}
	})
	input := map[string]any{"name": "retry-media", "image": "nginx:alpine", "port": 80, "nodeId": node.nodeID, "bindMounts": []map[string]any{{"source": "/srv/media", "target": "/media"}}}
	var row bindRequestResponse
	if code := postJSON(t, admin, base+"/api/bind-requests", input, &row); code != http.StatusCreated || row.Status != "failed" || row.AppID == 0 {
		t.Fatalf("partial failure: %d %+v", code, row)
	}
	firstID := row.AppID
	var app appResponse
	get(t, admin, base+"/api/apps/"+itoa(firstID), &app)
	if app.Running {
		t.Fatal("app deployed before DNS creation completed")
	}
	provider.fail.Store(false)
	if code := postJSON(t, admin, base+"/api/admin/bind-requests/"+itoa(row.ID)+"/retry", struct{}{}, &row); code != http.StatusOK || row.Status != "created" || row.AppID != firstID {
		t.Fatalf("retry: %d %+v", code, row)
	}
	select {
	case d := <-states:
		if len(d.Apps) != 1 || d.Apps[0].Id != firstID || !d.Apps[0].Running {
			t.Fatalf("retried deployment: %v", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no retry deployment")
	}
	var apps []appResponse
	get(t, admin, base+"/api/apps", &apps)
	if len(apps) != 1 {
		t.Fatalf("retry duplicated app: %+v", apps)
	}
}

func TestBindApprovalRechecksNameAtDecision(t *testing.T) {
	base, server, admin, _ := setupStackServer(t)
	owner := bindTestUser(t, server, base, "waiting-author", "user")
	node := connectFakeAgent(t, base, server, admin, "bind-names", func(h *agentpb.Hello) { h.BindMounts = true })
	node.desired(t)
	_ = answerBindChecks(node, func(check *agentpb.ValidateBindMounts) *agentpb.BindValidationResult {
		return &agentpb.BindValidationResult{Mounts: check.Mounts}
	})
	input := map[string]any{"name": "taken-later", "image": "nginx:alpine", "port": 80, "nodeId": node.nodeID, "bindMounts": []map[string]any{{"source": "/srv/media", "target": "/media"}}}
	var row bindRequestResponse
	postJSON(t, owner, base+"/api/bind-requests", input, &row)
	if code := postJSON(t, admin, base+"/api/apps", appInput{Name: "taken-later", Image: "nginx:alpine", Port: 80}, nil); code != http.StatusCreated {
		t.Fatalf("direct creation: %d", code)
	}
	if code := postJSON(t, admin, base+"/api/admin/bind-requests/"+itoa(row.ID)+"/approve", struct{}{}, nil); code != http.StatusConflict {
		t.Fatalf("conflict: %d", code)
	}
	get(t, owner, base+"/api/bind-requests/"+itoa(row.ID), &row)
	if row.Status != "failed" || row.Reason == "" || row.AppID != 0 {
		t.Fatalf("request result: %+v", row)
	}
}

func TestBindApprovalPreservesDockerfileAndSandboxSources(t *testing.T) {
	for _, tc := range []struct {
		name           string
		config         map[string]any
		wantKind       string
		wantImage      string
		wantDockerfile string
		wantPort       int32
	}{
		{name: "Dockerfile", config: map[string]any{"name": "bound-dockerfile", "dockerfile": "FROM nginx:alpine\\nRUN echo approved", "port": 8080, "memoryMb": 256}, wantKind: "web", wantImage: builtImage("bound-dockerfile", "FROM nginx:alpine\\nRUN echo approved"), wantDockerfile: "FROM nginx:alpine\\nRUN echo approved", wantPort: 8080},
		{name: "Sandbox", config: map[string]any{"name": "bound-sandbox", "kind": "sandbox", "image": "debian:bookworm", "memoryMb": 256}, wantKind: "sandbox", wantImage: "debian:bookworm", wantPort: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, server, superadmin, _ := setupStackServer(t)
			node := connectFakeAgent(t, base, server, superadmin, "source-node", func(h *agentpb.Hello) { h.BindMounts = true })
			node.desired(t)
			states := answerBindChecks(node, func(check *agentpb.ValidateBindMounts) *agentpb.BindValidationResult {
				return &agentpb.BindValidationResult{Mounts: check.Mounts}
			})
			owner := bindTestUser(t, server, base, "source-owner", "user")
			input := map[string]any{"nodeId": node.nodeID, "bindMounts": []map[string]any{{"source": "/srv/files", "target": "/files"}}}
			for k, v := range tc.config {
				input[k] = v
			}
			var req bindRequestResponse
			if code := postJSON(t, owner, base+"/api/bind-requests", input, &req); code != http.StatusCreated {
				t.Fatalf("submit: %d", code)
			}
			if req.Status != "pending" || req.AppID != 0 {
				t.Fatalf("request created resource early: %+v", req)
			}
			var before []appResponse
			if code := get(t, owner, base+"/api/apps", &before); code != http.StatusOK || len(before) != 0 {
				t.Fatalf("app visible before approval: %d %+v", code, before)
			}
			var saved bindRequestResponse
			if code := get(t, owner, base+"/api/bind-requests/"+itoa(req.ID), &saved); code != http.StatusOK || saved.Config.Dockerfile != tc.wantDockerfile || saved.Config.Kind != tc.wantKind {
				t.Fatalf("saved source config: %d %+v", code, saved.Config)
			}
			url := base + "/api/admin/bind-requests/" + itoa(req.ID) + "/approve"
			if code := postJSON(t, superadmin, url, struct{}{}, &req); code != http.StatusOK || req.Status != "created" {
				t.Fatalf("approve: %d %+v", code, req)
			}
			select {
			case desired := <-states:
				if len(desired.Apps) != 1 {
					t.Fatalf("desired apps: %v", desired.Apps)
				}
				app := desired.Apps[0]
				if app.GetSandbox() != (tc.wantKind == "sandbox") || app.Image != tc.wantImage || app.Dockerfile != tc.wantDockerfile || app.Port != tc.wantPort || len(app.BindMounts) != 1 || !app.BindMounts[0].ReadOnly {
					t.Fatalf("approved source: %v", app)
				}
				if tc.wantKind == "sandbox" && (app.Hostname != "" || app.Sandbox == false) {
					t.Fatalf("sandbox must remain private and SSH-oriented: %v", app)
				}
				if tc.wantKind == "sandbox" && app.Running && app.Port != 0 {
					t.Fatalf("sandbox exposed web port: %v", app)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("no deployment after approval")
			}
		})
	}
}
