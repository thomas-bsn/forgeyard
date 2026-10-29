package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libdns/libdns"
	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/dns"
	"github.com/thomas-bsn/forgeyard/internal/dns/dnstest"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
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
	if code := get(t, owner, base+"/api/apps/"+itoa(row.AppID), &app); code != http.StatusOK || app.OwnerName != "lea" || app.NodeID != node.nodeID || len(app.EnvKeys) != 1 || app.EnvKeys[0] != "TOKEN" {
		t.Fatalf("created app: %d %+v", code, app)
	}
	var revealed envReveal
	if code := get(t, owner, base+"/api/apps/"+itoa(row.AppID)+"/env", &revealed); code != http.StatusOK || revealed.Env["TOKEN"] != "keep-secret" {
		t.Fatalf("revealed env: %d %+v", code, revealed)
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

func TestBindChangeKeepsOldAccessUntilSuperadminApproves(t *testing.T) {
	base, server, superadmin, _ := setupStackServer(t)
	node := connectFakeAgent(t, base, server, superadmin, "bind-change-node", func(h *agentpb.Hello) { h.BindMounts = true })
	node.desired(t)
	states := answerBindChecks(node, func(check *agentpb.ValidateBindMounts) *agentpb.BindValidationResult {
		return &agentpb.BindValidationResult{Mounts: check.Mounts}
	})
	owner := bindTestUser(t, server, base, "change-owner", "user")
	admin := bindTestUser(t, server, base, "change-admin", "admin")
	create := map[string]any{"name": "change-app", "image": "nginx:alpine", "port": 8080, "memoryMb": 128, "nodeId": node.nodeID, "bindMounts": []map[string]any{{"source": "/srv/old", "target": "/data", "readOnly": true}}}
	var request bindRequestResponse
	if code := postJSON(t, owner, base+"/api/bind-requests", create, &request); code != http.StatusCreated {
		t.Fatalf("create request: %d", code)
	}
	approve := base + "/api/admin/bind-requests/" + itoa(request.ID) + "/approve"
	if code := postJSON(t, superadmin, approve, struct{}{}, &request); code != http.StatusOK {
		t.Fatalf("create approve: %d", code)
	}
	appID := request.AppID
	select {
	case <-states:
	case <-time.After(3 * time.Second):
		t.Fatal("initial desired state missing")
	}
	change := map[string]any{"change": map[string]any{"resourceId": appID, "resourceType": "app", "nodeId": node.nodeID, "bindMounts": []map[string]any{{"source": "/srv/new", "target": "/data", "readOnly": false}}}}
	if code := postJSON(t, owner, base+"/api/bind-requests", change, &request); code != http.StatusCreated || request.Status != "pending" || request.Config.Change == nil {
		t.Fatalf("change submission: %d %+v", code, request)
	}
	var current appResponse
	if code := get(t, owner, base+"/api/apps/"+itoa(appID), &current); code != http.StatusOK || current.BindMounts[0].Source != "/srv/old" {
		t.Fatalf("pending change altered current mounts: %d %+v", code, current.BindMounts)
	}
	changeApprove := base + "/api/admin/bind-requests/" + itoa(request.ID) + "/approve"
	if code := postJSON(t, admin, changeApprove, struct{}{}, nil); code != http.StatusForbidden {
		t.Fatalf("ordinary admin approved: %d", code)
	}
	if code := postJSON(t, superadmin, changeApprove, struct{}{}, &request); code != http.StatusOK || request.Status != "created" {
		t.Fatalf("approve change: %d %+v", code, request)
	}
	if code := get(t, owner, base+"/api/apps/"+itoa(appID), &current); code != http.StatusOK || current.BindMounts[0].Source != "/srv/new" || current.BindMounts[0].ReadOnly == nil || *current.BindMounts[0].ReadOnly {
		t.Fatalf("approved change not applied: %d %+v", code, current.BindMounts)
	}
	// Resource was stopped before access cutover. Approval must not start it implicitly.
	if len(current.BindMounts) != 1 || current.Running {
		t.Fatalf("approval should leave app stopped: %+v", current)
	}
	if code := postJSON(t, owner, base+"/api/bind-requests", map[string]any{"change": map[string]any{"resourceId": appID, "resourceType": "app", "nodeId": node.nodeID, "bindMounts": []map[string]any{{"source": "/srv/third", "target": "/data"}}}}, &request); code != http.StatusCreated {
		t.Fatalf("submit second change: %d", code)
	}
	if code := postJSON(t, owner, base+"/api/bind-requests", map[string]any{"change": map[string]any{"resourceId": appID, "resourceType": "app", "nodeId": node.nodeID, "bindMounts": []map[string]any{{"source": "/srv/fourth", "target": "/data"}}}}, nil); code != http.StatusConflict {
		t.Fatalf("parallel pending change was accepted: %d", code)
	}
	if code := postJSON(t, superadmin, base+"/api/admin/apps/"+itoa(appID)+"/bind-mounts/revoke", struct{}{}, nil); code != http.StatusOK {
		t.Fatalf("revoke while change request pending: %d", code)
	}
	if code := postJSON(t, superadmin, base+"/api/admin/bind-requests/"+itoa(request.ID)+"/approve", struct{}{}, nil); code != http.StatusConflict {
		t.Fatalf("incompatible change request should fail visibly: %d", code)
	}
	if code := get(t, owner, base+"/api/bind-requests/"+itoa(request.ID), &request); code != http.StatusOK || request.Status != "failed" {
		t.Fatalf("failed change result is not visible: %d %+v", code, request)
	}
	if code := postJSON(t, owner, base+"/api/bind-requests/"+itoa(request.ID)+"/cancel", struct{}{}, &request); code != http.StatusOK || request.Status != "cancelled" {
		t.Fatalf("author could not cancel failed change: %d %+v", code, request)
	}
	pending, err := server.store.HasPendingBindChange(context.Background(), db.HasPendingBindChangeParams{ChangeResourceType: "app", ChangeResourceID: appID})
	if err != nil || pending != 0 {
		t.Fatalf("cancelled failed request still blocks a replacement: pending=%d err=%v", pending, err)
	}
}

func TestComposeBindChangeIsStagedAndAppliedAsOneProject(t *testing.T) {
	base, server, superadmin, _ := setupStackServer(t)
	node := connectFakeAgent(t, base, server, superadmin, "compose-change-node", func(h *agentpb.Hello) { h.BindMounts = true })
	node.desired(t)
	states := answerBindChecks(node, func(check *agentpb.ValidateBindMounts) *agentpb.BindValidationResult {
		return &agentpb.BindValidationResult{Mounts: check.Mounts}
	})
	owner := bindTestUser(t, server, base, "compose-change-owner", "user")
	composeText := "services:\n  web:\n    image: nginx:alpine\n    volumes:\n      - /srv/old:/data:ro\n    x-forgeyard:\n      expose: { port: 80 }\n  worker:\n    image: busybox:1.36\n    command: sleep 3600\n"
	var row bindRequestResponse
	if code := postJSON(t, owner, base+"/api/bind-requests", map[string]any{"nodeId": node.nodeID, "bindMounts": []any{}, "stack": map[string]any{"name": "compose-change", "compose": composeText}}, &row); code != http.StatusCreated {
		t.Fatalf("submit project: %d %+v", code, row)
	}
	if code := postJSON(t, superadmin, base+"/api/admin/bind-requests/"+itoa(row.ID)+"/approve", struct{}{}, &row); code != http.StatusOK {
		t.Fatalf("approve create: %d", code)
	}
	stackID := row.StackID
	target := connectFakeAgent(t, base, server, superadmin, "compose-change-target", func(h *agentpb.Hello) { h.BindMounts = true })
	target.desired(t)
	targetStates := answerBindChecks(target, func(check *agentpb.ValidateBindMounts) *agentpb.BindValidationResult {
		return &agentpb.BindValidationResult{Mounts: check.Mounts}
	})
	select {
	case <-states:
	case <-time.After(3 * time.Second):
		t.Fatal("initial project state missing")
	}
	newCompose := strings.Replace(composeText, "/srv/old:/data:ro", "/srv/new:/data:rw", 1)
	if code := postJSON(t, owner, base+"/api/stacks/"+itoa(stackID)+"/stop", struct{}{}, nil); code != http.StatusOK {
		t.Fatalf("stop project before changing node: %d", code)
	}
	change := map[string]any{"change": map[string]any{"resourceId": stackID, "resourceType": "stack", "nodeId": target.nodeID, "stack": map[string]any{"name": "compose-change", "compose": newCompose, "env": map[string]string{}}}}
	if code := postJSON(t, owner, base+"/api/bind-requests", change, &row); code != http.StatusCreated || row.Status != "pending" {
		t.Fatalf("submit change: %d %+v", code, row)
	}
	updatedCompose := strings.Replace(composeText, "nginx:alpine", "nginx:1.27-alpine", 1)
	if code := putJSON(t, owner, base+"/api/stacks/"+itoa(stackID), stackInput{Compose: updatedCompose}, nil); code != http.StatusOK {
		t.Fatalf("ordinary config edit while bind request is pending: %d", code)
	}
	var before stackResponse
	if code := get(t, owner, base+"/api/stacks/"+itoa(stackID), &before); code != http.StatusOK || before.Services[0].BindMounts[0].Source != "/srv/old" {
		t.Fatalf("pending changed current stack: %d %+v", code, before.Services)
	}
	if code := postJSON(t, owner, base+"/api/stacks/"+itoa(stackID)+"/stop", struct{}{}, nil); code != http.StatusOK {
		t.Fatalf("stop updated project before approval: %d", code)
	}
	if code := postJSON(t, superadmin, base+"/api/admin/bind-requests/"+itoa(row.ID)+"/approve", struct{}{}, &row); code != http.StatusOK || row.Status != "created" {
		t.Fatalf("approve change: %d %+v", code, row)
	}
	var after stackResponse
	if code := get(t, owner, base+"/api/stacks/"+itoa(stackID), &after); code != http.StatusOK || after.NodeID != target.nodeID || after.Services[0].NodeID != target.nodeID || after.Services[0].Image != "nginx:1.27-alpine" || after.Services[0].BindMounts[0].Source != "/srv/new" || after.Services[0].BindMounts[0].ReadOnly == nil || *after.Services[0].BindMounts[0].ReadOnly {
		t.Fatalf("changed project config: %d %+v", code, after.Services)
	}
	select {
	case desired := <-targetStates:
		if len(desired.Apps) != 2 {
			t.Fatalf("target node did not receive all project services: %+v", desired.Apps)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("target node did not receive project desired state")
	}
}

func TestComposeBindRequestStagesNoProjectUntilSuperadminApproval(t *testing.T) {
	base, server, superadmin, _ := setupStackServer(t)
	node := connectFakeAgent(t, base, server, superadmin, "compose-bind-node", func(h *agentpb.Hello) { h.BindMounts = true })
	node.desired(t)
	states := answerBindChecks(node, func(check *agentpb.ValidateBindMounts) *agentpb.BindValidationResult {
		return &agentpb.BindValidationResult{Mounts: check.Mounts}
	})
	owner := bindTestUser(t, server, base, "compose-owner", "user")
	composeText := `services:
  db:
    image: postgres:17-alpine
    volumes:
      - data:/var/lib/postgresql/data
      - /srv/postgres:/backup:ro
    environment:
      POSTGRES_PASSWORD: ${DB_PASSWORD}
  web:
    image: nginx:alpine
    volumes:
      - type: bind
        source: /srv/site
        target: /site
        read_only: false
volumes:
  data:
`
	input := map[string]any{"nodeId": node.nodeID, "stack": map[string]any{"name": "archive", "compose": composeText, "env": map[string]string{"DB_PASSWORD": "secret-db"}, "registry": map[string]string{"server": "ghcr.io", "username": "registry-user", "password": "registry-secret"}}}
	var request bindRequestResponse
	if code := postJSON(t, owner, base+"/api/bind-requests", input, &request); code != http.StatusCreated || request.Status != "pending" {
		t.Fatalf("submit compose request: %d %+v", code, request)
	}
	if request.Config.Stack == nil || len(request.Config.BindMounts) != 2 || request.Config.Env != nil {
		t.Fatalf("frozen config or mount list: %+v", request.Config)
	}
	if code := postJSON(t, owner, base+"/api/admin/bind-requests/"+itoa(request.ID)+"/approve", struct{}{}, nil); code != http.StatusForbidden {
		t.Fatalf("ordinary user approved compose request: %d", code)
	}
	var verified []bindMount
	if code := postJSON(t, superadmin, base+"/api/admin/bind-requests/"+itoa(request.ID)+"/validate", struct{}{}, &verified); code != http.StatusOK || len(verified) != 2 || verified[0].Service != "db" || verified[1].Service != "web" {
		t.Fatalf("per-service validation view: %d %+v", code, verified)
	}
	stacks, err := server.store.ListStacksByOwnerID(context.Background(), request.AuthorID)
	if err != nil {
		t.Fatal(err)
	}
	apps, _ := server.store.ListAppsForNode(context.Background(), node.nodeID)
	if len(stacks) != 0 || len(apps) != 0 {
		t.Fatalf("resources existed before approval: stacks=%d apps=%d", len(stacks), len(apps))
	}
	var revealed map[string]string
	if code := postJSON(t, superadmin, base+"/api/admin/bind-requests/"+itoa(request.ID)+"/reveal", struct{}{}, &revealed); code != http.StatusOK || revealed["DB_PASSWORD"] != "secret-db" || revealed["registry.password"] != "registry-secret" {
		t.Fatalf("secret reveal: %d %+v", code, revealed)
	}
	if code := postJSON(t, superadmin, base+"/api/admin/bind-requests/"+itoa(request.ID)+"/approve", struct{}{}, &request); code != http.StatusOK || request.Status != "created" || request.StackID == 0 {
		t.Fatalf("approve compose request: %d %+v", code, request)
	}
	var created stackResponse
	if code := get(t, owner, base+"/api/stacks/"+itoa(request.StackID), &created); code != http.StatusOK || len(created.Volumes) != 1 || created.Volumes[0].Name != "data" {
		t.Fatalf("bind mounts must not become stack volumes: %d %+v", code, created.Volumes)
	}
	select {
	case desired := <-states:
		byService := map[string]*agentpb.AppSpec{}
		for _, app := range desired.Apps {
			if app.Stack != nil {
				byService[app.Stack.Service] = app
			}
		}
		if len(byService) != 2 {
			t.Fatalf("desired services: %+v", byService)
		}
		if db := byService["db"]; db.GetEnv()["POSTGRES_PASSWORD"] != "secret-db" || db.Stack.RegistryPassword != "registry-secret" || len(db.BindMounts) != 1 || db.BindMounts[0].Target != "/backup" || !db.BindMounts[0].ReadOnly || len(db.Stack.Volumes) != 1 {
			t.Fatalf("database service mounts or env: %+v", db)
		}
		web := byService["web"]
		if len(web.BindMounts) != 1 || web.BindMounts[0].Target != "/site" || web.BindMounts[0].ReadOnly {
			t.Fatalf("web binds: %+v", web.BindMounts)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no desired state after approval")
	}
	var updated stackResponse
	if code := putJSON(t, owner, base+"/api/stacks/"+itoa(request.StackID), stackInput{Compose: composeText, Env: map[string]string{"DB_PASSWORD": "updated-secret"}}, &updated); code != http.StatusOK {
		t.Fatalf("ordinary compose edit with unchanged approved binds: %d", code)
	}
	select {
	case desired := <-states:
		for _, app := range desired.Apps {
			if app.Stack != nil && app.Stack.GetService() == "db" && (app.GetEnv()["POSTGRES_PASSWORD"] != "updated-secret" || len(app.BindMounts) != 1 || !app.GetBindAuthorized()) {
				t.Fatalf("normal stack update lost approved bind access: %+v", app)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no desired state after normal stack update")
	}
	revokeURL := base + "/api/admin/stacks/" + itoa(request.StackID) + "/bind-mounts/revoke"
	if code := postJSON(t, owner, revokeURL, struct{}{}, nil); code != http.StatusForbidden {
		t.Fatalf("ordinary user revoked project binds: %d", code)
	}
	var revoked stackResponse
	if code := postJSON(t, superadmin, revokeURL, struct{}{}, &revoked); code != http.StatusOK || len(revoked.Services) != 2 {
		t.Fatalf("revoke project binds: %d %+v", code, revoked)
	}
	if code := postJSON(t, superadmin, base+"/api/stacks/"+itoa(request.StackID)+"/start", struct{}{}, nil); code != http.StatusConflict {
		t.Fatalf("revoked project restarted: %d", code)
	}
	if code := putJSON(t, owner, base+"/api/stacks/"+itoa(request.StackID), stackInput{Compose: composeText, Env: map[string]string{"DB_PASSWORD": "secret-db"}}, nil); code != http.StatusConflict {
		t.Fatalf("revoked project update restarted bind service: %d", code)
	}
	reauthorizedCompose := strings.Replace(composeText, "/srv/postgres:/backup:ro", "/srv/postgres-new:/backup:rw", 1)
	change := map[string]any{"change": map[string]any{"resourceId": request.StackID, "resourceType": "stack", "nodeId": node.nodeID, "stack": map[string]any{"name": "archive", "compose": reauthorizedCompose, "env": map[string]string{"DB_PASSWORD": "secret-db"}}}}
	if code := postJSON(t, owner, base+"/api/bind-requests", change, nil); code != http.StatusConflict {
		t.Fatalf("same-node reauthorization bypassed revocation: %d", code)
	}
	select {
	case desired := <-states:
		for _, app := range desired.Apps {
			if app.Stack != nil && (app.GetBindAuthorized() || app.GetRunning()) {
				t.Fatalf("bind service was not stopped and revoked: %+v", app)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no desired state after project revocation")
	}
	selfCompose := "services:\n  files:\n    image: nginx:alpine\n    volumes:\n      - /srv/self:/files\n"
	var selfRequest bindRequestResponse
	if code := postJSON(t, superadmin, base+"/api/bind-requests", map[string]any{"nodeId": node.nodeID, "stack": map[string]any{"name": "self-approved", "compose": selfCompose}}, &selfRequest); code != http.StatusCreated || selfRequest.Status != "created" || selfRequest.StackID == 0 {
		t.Fatalf("superadmin compose auto-approval: %d %+v", code, selfRequest)
	}
}

func TestComposeBindRetryResumesSameStackAfterDNSFailure(t *testing.T) {
	base, server, superadmin, _ := setupStackServer(t)
	node := connectFakeAgent(t, base, server, superadmin, "compose-retry-node", func(h *agentpb.Hello) { h.BindMounts = true })
	node.desired(t)
	states := answerBindChecks(node, func(check *agentpb.ValidateBindMounts) *agentpb.BindValidationResult {
		return &agentpb.BindValidationResult{Mounts: check.Mounts}
	})
	owner := bindTestUser(t, server, base, "compose-retry-owner", "user")
	provider := &failingBindDNS{Provider: dnstest.New("example.com.")}
	provider.fail.Store(true)
	server.newDNSProvider = func(string, map[string]string) (dns.Provider, error) { return provider, nil }
	composeText := "services:\n  web:\n    image: nginx:alpine\n    volumes:\n      - /srv/web:/site:ro\n    x-forgeyard:\n      expose: { port: 80 }\n"
	var request bindRequestResponse
	if code := postJSON(t, owner, base+"/api/bind-requests", map[string]any{"nodeId": node.nodeID, "stack": map[string]any{"name": "retry-project", "compose": composeText}}, &request); code != http.StatusCreated {
		t.Fatalf("submit: %d %+v", code, request)
	}
	approve := base + "/api/admin/bind-requests/" + itoa(request.ID) + "/approve"
	if code := postJSON(t, superadmin, approve, struct{}{}, nil); code != http.StatusConflict {
		t.Fatalf("expected staged failure: %d", code)
	}
	if code := get(t, owner, base+"/api/bind-requests/"+itoa(request.ID), &request); code != http.StatusOK || request.Status != "failed" || request.StackID == 0 {
		t.Fatalf("failed request state: %d %+v", code, request)
	}
	stackID := request.StackID
	stacks, err := server.store.ListStacksByOwnerID(context.Background(), request.AuthorID)
	if err != nil || len(stacks) != 1 {
		t.Fatalf("stack staging: %v %+v", err, stacks)
	}
	provider.fail.Store(false)
	if code := postJSON(t, superadmin, base+"/api/admin/bind-requests/"+itoa(request.ID)+"/retry", struct{}{}, &request); code != http.StatusOK || request.Status != "created" || request.StackID != stackID {
		t.Fatalf("retry: %d %+v", code, request)
	}
	stacks, err = server.store.ListStacksByOwnerID(context.Background(), request.AuthorID)
	if err != nil || len(stacks) != 1 {
		t.Fatalf("retry duplicated project: %v %+v", err, stacks)
	}
	apps, err := server.store.ListStackApps(context.Background(), stackID)
	if err != nil || len(apps) != 1 {
		t.Fatalf("retry duplicated services: %v %+v", err, apps)
	}
	select {
	case <-states:
	case <-time.After(5 * time.Second):
		t.Fatal("no desired state after stack retry")
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

func TestSuperadminBindSubmissionAutoApprovesDockerfileAndSandbox(t *testing.T) {
	for _, tc := range []struct {
		name, kind, image, dockerfile string
		port                          int
	}{
		{name: "root-file", kind: kindWeb, dockerfile: "FROM alpine:3.21\nRUN echo ok", port: 8080},
		{name: "root-box", kind: kindSandbox, image: "debian:bookworm"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, server, superadmin, _ := setupStackServer(t)
			node := connectFakeAgent(t, base, server, superadmin, "auto-source-node", func(h *agentpb.Hello) { h.BindMounts = true })
			node.desired(t)
			states := answerBindChecks(node, func(check *agentpb.ValidateBindMounts) *agentpb.BindValidationResult {
				return &agentpb.BindValidationResult{Mounts: check.Mounts}
			})
			input := map[string]any{"name": tc.name, "kind": tc.kind, "image": tc.image, "dockerfile": tc.dockerfile, "port": tc.port, "nodeId": node.nodeID, "bindMounts": []map[string]any{{"source": "/srv/files", "target": "/files"}}}
			var request bindRequestResponse
			if code := postJSON(t, superadmin, base+"/api/bind-requests", input, &request); code != http.StatusCreated || request.Status != "created" || request.AppID == 0 {
				t.Fatalf("auto-approved request: %d %+v", code, request)
			}
			select {
			case desired := <-states:
				app := desired.Apps[0]
				wantImage := tc.image
				if tc.dockerfile != "" {
					wantImage = builtImage(tc.name, tc.dockerfile)
				}
				if app.Image != wantImage || app.Dockerfile != tc.dockerfile || app.GetSandbox() != (tc.kind == kindSandbox) || len(app.BindMounts) != 1 {
					t.Fatalf("auto-approved source spec: %v", app)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("auto-approval did not deploy")
			}
		})
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
			change := map[string]any{"change": map[string]any{"resourceId": req.AppID, "resourceType": "app", "nodeId": node.nodeID, "bindMounts": []map[string]any{{"source": "/srv/updated-files", "target": "/files", "readOnly": false}}}}
			if code := postJSON(t, owner, base+"/api/bind-requests", change, &req); code != http.StatusCreated {
				t.Fatalf("submit %s change: %d %+v", tc.name, code, req)
			}
			var current appResponse
			if code := get(t, owner, base+"/api/apps/"+itoa(req.AppID), &current); code != http.StatusOK || current.BindMounts[0].Source != "/srv/files" {
				t.Fatalf("pending %s change altered current access: %d %+v", tc.name, code, current.BindMounts)
			}
			if code := postJSON(t, superadmin, base+"/api/admin/bind-requests/"+itoa(req.ID)+"/approve", struct{}{}, &req); code != http.StatusOK || req.Status != "created" {
				t.Fatalf("approve %s change: %d %+v", tc.name, code, req)
			}
			if code := get(t, owner, base+"/api/apps/"+itoa(req.AppID), &current); code != http.StatusOK || current.BindMounts[0].Source != "/srv/updated-files" || current.BindMounts[0].ReadOnly == nil || *current.BindMounts[0].ReadOnly || current.Kind != tc.wantKind || current.Image != tc.wantImage || current.Dockerfile != tc.wantDockerfile {
				t.Fatalf("approved %s change altered source config or access: %d %+v", tc.name, code, current)
			}
		})
	}
}

// A project made without host folders can ask for one afterwards: the owner submits the Compose with the
// folder, and only the superadmin's approval adds it.
func TestProjectWithoutBindMountsCanRequestOne(t *testing.T) {
	base, server, superadmin, _ := setupStackServer(t)
	node := connectFakeAgent(t, base, server, superadmin, "late-bind-node", func(h *agentpb.Hello) { h.BindMounts = true })
	node.desired(t)
	answerBindChecks(node, func(check *agentpb.ValidateBindMounts) *agentpb.BindValidationResult {
		return &agentpb.BindValidationResult{Mounts: check.Mounts}
	})
	owner := bindTestUser(t, server, base, "late-owner", "user")
	composeText := "services:\n  web:\n    image: nginx:alpine\n    x-forgeyard:\n      expose: { port: 80 }\n"
	var st stackResponse
	if code := postJSON(t, owner, base+"/api/stacks", stackInput{Name: "late-bind", Compose: composeText}, &st); code != http.StatusCreated {
		t.Fatalf("create project: %d", code)
	}

	// Editing the Compose to add the folder directly is still refused.
	withBind := strings.Replace(composeText, "    image: nginx:alpine\n", "    image: nginx:alpine\n    volumes:\n      - /srv/late:/data:ro\n", 1)
	if code := putJSON(t, owner, base+"/api/stacks/"+itoa(st.ID), stackInput{Compose: withBind}, nil); code != http.StatusBadRequest {
		t.Fatalf("direct edit adding a host folder: %d", code)
	}
	// A request without any folder is refused too.
	if code := postJSON(t, owner, base+"/api/bind-requests", map[string]any{"change": map[string]any{"resourceId": st.ID, "resourceType": "stack", "nodeId": node.nodeID, "stack": map[string]any{"name": "late-bind", "compose": composeText}}}, nil); code != http.StatusBadRequest {
		t.Fatalf("request without a folder: %d", code)
	}

	var row bindRequestResponse
	if code := postJSON(t, owner, base+"/api/bind-requests", map[string]any{"change": map[string]any{"resourceId": st.ID, "resourceType": "stack", "nodeId": node.nodeID, "stack": map[string]any{"name": "late-bind", "compose": withBind}}}, &row); code != http.StatusCreated || row.Status != "pending" {
		t.Fatalf("request the folder: %d %+v", code, row)
	}
	if code := postJSON(t, owner, base+"/api/admin/bind-requests/"+itoa(row.ID)+"/approve", struct{}{}, nil); code != http.StatusForbidden {
		t.Fatalf("the owner approving: %d", code)
	}
	if code := postJSON(t, superadmin, base+"/api/admin/bind-requests/"+itoa(row.ID)+"/approve", struct{}{}, &row); code != http.StatusOK || row.Status != "created" {
		t.Fatalf("approve: %d %+v", code, row)
	}
	var after stackResponse
	if code := get(t, owner, base+"/api/stacks/"+itoa(st.ID), &after); code != http.StatusOK || len(after.Services) != 1 ||
		len(after.Services[0].BindMounts) != 1 || after.Services[0].BindMounts[0].Source != "/srv/late" || !after.Services[0].BindAuthorized {
		t.Fatalf("project after approval: %d %+v", code, after.Services)
	}
}
