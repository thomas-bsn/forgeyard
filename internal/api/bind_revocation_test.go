package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
)

func TestBindRevocationPersistsAndBlocksRestart(t *testing.T) {
	base, server, superadmin, _ := setupStackServer(t)
	node := connectFakeAgent(t, base, server, superadmin, "revoke-node", func(h *agentpb.Hello) { h.BindMounts = true })
	node.desired(t)
	states := answerBindChecks(node, func(check *agentpb.ValidateBindMounts) *agentpb.BindValidationResult {
		return &agentpb.BindValidationResult{Mounts: check.Mounts}
	})
	owner := bindTestUser(t, server, base, "mount-owner", "user")
	staff := bindTestUser(t, server, base, "mount-admin", "admin")
	input := map[string]any{"name": "revocable", "image": "alpine:3.21", "port": 80, "nodeId": node.nodeID, "bindMounts": []map[string]any{{"source": "/srv/media", "target": "/media"}}}
	var request bindRequestResponse
	if code := postJSON(t, owner, base+"/api/bind-requests", input, &request); code != http.StatusCreated {
		t.Fatalf("submit: %d", code)
	}
	if code := postJSON(t, superadmin, base+"/api/admin/bind-requests/"+itoa(request.ID)+"/approve", struct{}{}, &request); code != http.StatusOK {
		t.Fatalf("approve: %d", code)
	}
	<-states
	url := base + "/api/admin/apps/" + itoa(request.AppID) + "/bind-mounts/revoke"
	for _, client := range []*http.Client{owner} {
		if code := postJSON(t, client, url, struct{}{}, nil); code != http.StatusForbidden {
			t.Fatalf("unauthorized revoke: %d", code)
		}
	}
	var app appResponse
	if code := postJSON(t, staff, url, struct{}{}, &app); code != http.StatusOK {
		t.Fatalf("revoke: %d", code)
	}
	if app.BindAuthorized || app.Running || len(app.BindMounts) != 1 {
		t.Fatalf("revoked authorization: %+v", app)
	}
	select {
	case desired := <-states:
		if len(desired.Apps) != 1 || desired.Apps[0].Running || desired.Apps[0].BindAuthorized {
			t.Fatalf("revoked desired: %v", desired)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("revocation not pushed")
	}
	for _, client := range []*http.Client{owner, staff, superadmin} {
		for _, action := range []string{"start", "redeploy"} {
			if code := postJSON(t, client, base+"/api/apps/"+itoa(app.ID)+"/"+action, struct{}{}, nil); code != http.StatusConflict {
				t.Fatalf("revoked %s: %d", action, code)
			}
		}
	}
	// Configuration can still be maintained, but updating it cannot revive a revoked authorization.
	if code := putJSON(t, owner, base+"/api/apps/"+itoa(app.ID), map[string]any{"image": "alpine:3.21", "port": 8080, "memoryMb": 128}, &app); code != http.StatusOK || app.BindAuthorized || app.Running {
		t.Fatalf("revoked update: %d %+v", code, app)
	}
	if code := postJSON(t, superadmin, url, struct{}{}, &app); code != http.StatusOK || app.BindAuthorized {
		t.Fatalf("repeat revoke: %d %+v", code, app)
	}
	if code := get(t, owner, base+"/api/apps/"+itoa(app.ID), &app); code != http.StatusOK || app.BindAuthorized || app.Running {
		t.Fatalf("persisted revoke: %d %+v", code, app)
	}
	// Repeating the original creation approval cannot revive a revoked authorization either.
	if code := postJSON(t, superadmin, base+"/api/admin/bind-requests/"+itoa(request.ID)+"/approve", struct{}{}, nil); code != http.StatusOK {
		t.Fatalf("repeat creation approval: %d", code)
	}
	get(t, owner, base+"/api/apps/"+itoa(app.ID), &app)
	if app.BindAuthorized || app.Running {
		t.Fatalf("old approval revived authorization: %+v", app)
	}
	var ordinary appResponse
	if code := postJSON(t, superadmin, base+"/api/apps", appInput{Name: "ordinary", Image: "alpine:3.21", Port: 80, NodeID: node.nodeID}, &ordinary); code != http.StatusCreated {
		t.Fatalf("plain creation: %d", code)
	}
	if code := postJSON(t, superadmin, base+"/api/admin/apps/"+itoa(ordinary.ID)+"/bind-mounts/revoke", struct{}{}, nil); code != http.StatusConflict {
		t.Fatalf("plain revoke: %d", code)
	}
	if code := postJSON(t, superadmin, base+"/api/apps/"+itoa(ordinary.ID)+"/redeploy", struct{}{}, &ordinary); code != http.StatusOK || !ordinary.Running {
		t.Fatalf("plain app affected: %d %+v", code, ordinary)
	}
}

func TestBindRevocationWhileOfflineAppliesAtReconnect(t *testing.T) {
	base, server, admin, _ := setupStackServer(t)
	node := connectFakeAgent(t, base, server, admin, "offline-revoke", func(h *agentpb.Hello) { h.BindMounts = true })
	node.desired(t)
	states := answerBindChecks(node, func(check *agentpb.ValidateBindMounts) *agentpb.BindValidationResult {
		return &agentpb.BindValidationResult{Mounts: check.Mounts}
	})
	var row bindRequestResponse
	input := map[string]any{"name": "offline-access", "image": "alpine:3.21", "port": 80, "nodeId": node.nodeID, "bindMounts": []map[string]any{{"source": "/srv/media", "target": "/media"}}}
	if code := postJSON(t, admin, base+"/api/bind-requests", input, &row); code != http.StatusCreated || row.Status != "created" {
		t.Fatalf("autoapprove: %d %+v", code, row)
	}
	<-states
	if err := node.stream.CloseSend(); err != nil {
		t.Fatal(err)
	}
	var app appResponse
	deadline := time.Now().Add(5 * time.Second)
	for {
		get(t, admin, base+"/api/apps/"+itoa(row.AppID), &app)
		if app.State == "node-offline" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("node did not disconnect: %+v", app)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if code := postJSON(t, admin, base+"/api/admin/apps/"+itoa(app.ID)+"/bind-mounts/revoke", struct{}{}, &app); code != http.StatusOK || app.BindAuthorized || app.Running || app.State != "node-offline" {
		t.Fatalf("offline revoke: %d %+v", code, app)
	}
	stream, err := node.client.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.CloseSend()
	if err = stream.Send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_Hello{Hello: &agentpb.Hello{BindMounts: true, Info: &agentpb.NodeInfo{Hostname: "offline-revoke"}}}}); err != nil {
		t.Fatal(err)
	}
	if first, err := stream.Recv(); err != nil || first.GetWelcome() == nil {
		t.Fatalf("welcome: %v %v", first, err)
	}
	message, err := stream.Recv()
	if err != nil || message.GetDesiredState() == nil || len(message.GetDesiredState().Apps) != 1 {
		t.Fatalf("reconnect desired: %v %v", message, err)
	}
	if spec := message.GetDesiredState().Apps[0]; spec.Running || spec.BindAuthorized || len(spec.BindMounts) != 1 {
		t.Fatalf("reconnect revived access: %v", spec)
	}
}
