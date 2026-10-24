package api

import (
	"encoding/json"
	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestBindRequestNotificationsSubmissionAndRefusal(t *testing.T) {
	base, server, superadmin, node := setupStackServer(t)
	owner := bindTestUser(t, server, base, "request-owner", "user")
	hooks := &fakeWebhooks{}
	hooks.install(t, server)
	if code := putJSON(t, superadmin, base+"/api/admin/settings/notifications", notifySettings{Webhook: "https://discord.com/api/webhooks/1/review", Events: map[string]bool{"requests": true}}, nil); code != http.StatusOK {
		t.Fatalf("review notifications: %d", code)
	}
	if code := putJSON(t, owner, base+"/api/me/notifications", map[string]string{"webhook": "https://discord.com/api/webhooks/2/owner"}, nil); code != http.StatusOK {
		t.Fatalf("owner notifications: %d", code)
	}
	var row bindRequestResponse
	input := map[string]any{"name": "private-app-name", "image": "registry.example/private-image", "port": 8080, "nodeId": node.nodeID, "env": map[string]string{"SECRET_KEY": "hidden-secret-value"}, "bindMounts": []map[string]any{{"source": "/srv/private-host-data", "target": "/private-container-data"}}}
	if code := postJSON(t, owner, base+"/api/bind-requests", input, &row); code != http.StatusCreated {
		t.Fatalf("submission: %d", code)
	}
	hooks.waitTitle(t, "/api/webhooks/1/review", "à examiner")
	hooks.waitTitle(t, "/api/webhooks/2/owner", "soumise")
	if code := postJSON(t, superadmin, base+"/api/admin/bind-requests/"+itoa(row.ID)+"/refuse", map[string]string{"reason": "private-rejection-reason /srv/private-host-data"}, &row); code != http.StatusOK || row.Status != "refused" {
		t.Fatalf("refusal: %d %+v", code, row)
	}
	hooks.waitTitle(t, "/api/webhooks/1/review", "refusée")
	hooks.waitTitle(t, "/api/webhooks/2/owner", "refusée")
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	for i, post := range hooks.posts {
		raw, _ := json.Marshal(post)
		for _, secret := range []string{"private-app-name", "private-image", "SECRET_KEY", "hidden-secret-value", "/srv/private-host-data", "/private-container-data", "private-rejection-reason"} {
			if strings.Contains(string(raw), secret) {
				t.Fatalf("notification leaks %q: %s", secret, raw)
			}
		}
		embed := post["embeds"].([]any)[0].(map[string]any)
		expected := "https://forgeyard.example.com/#/bind-requests/" + itoa(row.ID)
		if hooks.paths[i] == "/api/webhooks/1/review" {
			expected = "https://forgeyard.example.com/#/bind-requests/review/" + itoa(row.ID)
		}
		if embed["url"] != expected {
			t.Fatalf("wrong recipient link: %v, want %s", embed["url"], expected)
		}
	}
}

func TestBindNotificationsDoNotGateCreationWhenDiscordFails(t *testing.T) {
	base, server, superadmin, _ := setupStackServer(t)
	owner := bindTestUser(t, server, base, "request-owner", "user")
	hooks := &fakeWebhooks{status: http.StatusInternalServerError}
	hooks.install(t, server)
	putJSON(t, superadmin, base+"/api/admin/settings/notifications", notifySettings{Webhook: "https://discord.com/api/webhooks/1/review", Events: map[string]bool{"requests": true}}, nil)
	putJSON(t, owner, base+"/api/me/notifications", map[string]string{"webhook": "https://discord.com/api/webhooks/2/owner"}, nil)
	node := connectFakeAgent(t, base, server, superadmin, "bind-node", func(h *agentpb.Hello) { h.BindMounts = true })
	node.desired(t)
	states := answerBindChecks(node, func(check *agentpb.ValidateBindMounts) *agentpb.BindValidationResult {
		return &agentpb.BindValidationResult{Mounts: check.Mounts}
	})
	input := map[string]any{"name": "unblocked-app", "image": "nginx:alpine", "port": 80, "nodeId": node.nodeID, "bindMounts": []map[string]any{{"source": "/srv/media", "target": "/media"}}}
	var row bindRequestResponse
	if code := postJSON(t, owner, base+"/api/bind-requests", input, &row); code != http.StatusCreated {
		t.Fatalf("submission: %d", code)
	}
	approve := base + "/api/admin/bind-requests/" + itoa(row.ID) + "/approve"
	if code := postJSON(t, superadmin, approve, struct{}{}, &row); code != http.StatusOK || row.Status != "created" {
		t.Fatalf("Discord failure changed creation: %d %+v", code, row)
	}
	<-states
	hooks.waitTitle(t, "/api/webhooks/1/review", "enregistrée")
	hooks.waitTitle(t, "/api/webhooks/2/owner", "enregistrée")
	var got bindRequestResponse
	if code := get(t, owner, base+"/api/bind-requests/"+itoa(row.ID), &got); code != http.StatusOK || got.Status != "created" || got.AppID != row.AppID {
		t.Fatalf("notification failure changed persisted result: %d %+v", code, got)
	}
	var apps []appResponse
	get(t, owner, base+"/api/apps", &apps)
	if len(apps) != 1 {
		t.Fatalf("notification failure retried creation: %+v", apps)
	}
}

func TestBindRequestNotificationsOptionalAndRespectRequestPreference(t *testing.T) {
	for _, mode := range []string{"unconfigured", "requests-disabled"} {
		t.Run(mode, func(t *testing.T) {
			base, server, superadmin, node := setupStackServer(t)
			owner := bindTestUser(t, server, base, "request-owner", "user")
			hooks := &fakeWebhooks{}
			hooks.install(t, server)
			if mode == "requests-disabled" {
				putJSON(t, superadmin, base+"/api/admin/settings/notifications", notifySettings{Webhook: "https://discord.com/api/webhooks/1/review", Events: map[string]bool{"requests": false}}, nil)
				putJSON(t, owner, base+"/api/me/notifications", map[string]string{"webhook": "https://discord.com/api/webhooks/2/owner"}, nil)
			}
			input := map[string]any{"name": "optional-notifications", "image": "nginx:alpine", "port": 80, "nodeId": node.nodeID, "bindMounts": []map[string]any{{"source": "/srv/media", "target": "/media"}}}
			var row bindRequestResponse
			if code := postJSON(t, owner, base+"/api/bind-requests", input, &row); code != http.StatusCreated {
				t.Fatalf("submission: %d", code)
			}
			if code := postJSON(t, superadmin, base+"/api/admin/bind-requests/"+itoa(row.ID)+"/refuse", map[string]string{"reason": "Refus de test"}, &row); code != http.StatusOK || row.Status != "refused" {
				t.Fatalf("refusal without notifications: %d %+v", code, row)
			}
			// Negative observation at the external webhook boundary, allowing asynchronous delivery to run.
			time.Sleep(200 * time.Millisecond)
			hooks.mu.Lock()
			defer hooks.mu.Unlock()
			if len(hooks.posts) != 0 {
				t.Fatalf("unexpected notification for %s: %v", mode, hooks.posts)
			}
		})
	}
}

func TestBindRequestNotificationsReachSuperadminWithoutNotifyingOtherAdmins(t *testing.T) {
	base, server, superadmin, node := setupStackServer(t)
	owner := bindTestUser(t, server, base, "request-owner", "user")
	staff := bindTestUser(t, server, base, "ordinary-admin", "admin")
	hooks := &fakeWebhooks{}
	hooks.install(t, server)
	for _, recipient := range []struct {
		client *http.Client
		path   string
	}{{owner, "owner"}, {superadmin, "superadmin"}, {staff, "staff"}} {
		if code := putJSON(t, recipient.client, base+"/api/me/notifications", map[string]string{"webhook": "https://discord.com/api/webhooks/3/" + recipient.path}, nil); code != http.StatusOK {
			t.Fatalf("recipient webhook: %d", code)
		}
	}
	var row bindRequestResponse
	input := map[string]any{"name": "targeted-request", "image": "nginx:alpine", "port": 80, "nodeId": node.nodeID, "bindMounts": []map[string]any{{"source": "/srv/media", "target": "/media"}}}
	postJSON(t, owner, base+"/api/bind-requests", input, &row)
	hooks.waitTitle(t, "/api/webhooks/3/owner", "soumise")
	hooks.waitTitle(t, "/api/webhooks/3/superadmin", "à examiner")
	if code := postJSON(t, owner, base+"/api/bind-requests/"+itoa(row.ID)+"/cancel", struct{}{}, &row); code != http.StatusOK || row.Status != "cancelled" {
		t.Fatalf("cancel: %d %+v", code, row)
	}
	hooks.waitTitle(t, "/api/webhooks/3/owner", "annulée")
	hooks.waitTitle(t, "/api/webhooks/3/superadmin", "annulée")
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	for i, path := range hooks.paths {
		if path == "/api/webhooks/3/staff" {
			t.Fatalf("ordinary admin received personal review notification: %v", hooks.posts[i])
		}
		if path == "/api/webhooks/3/superadmin" {
			embed := hooks.posts[i]["embeds"].([]any)[0].(map[string]any)
			if embed["url"] != "https://forgeyard.example.com/#/bind-requests/review/"+itoa(row.ID) {
				t.Fatalf("superadmin not linked to review: %v", embed)
			}
		}
	}
}

func TestBindRequestNotificationsApprovalAndCreationResult(t *testing.T) {
	base, server, superadmin, _ := setupStackServer(t)
	owner := bindTestUser(t, server, base, "request-owner", "user")
	hooks := &fakeWebhooks{}
	hooks.install(t, server)
	putJSON(t, superadmin, base+"/api/admin/settings/notifications", notifySettings{Webhook: "https://discord.com/api/webhooks/1/review", Events: map[string]bool{"requests": true}}, nil)
	putJSON(t, owner, base+"/api/me/notifications", map[string]string{"webhook": "https://discord.com/api/webhooks/2/owner"}, nil)
	node := connectFakeAgent(t, base, server, superadmin, "bind-node", func(h *agentpb.Hello) { h.BindMounts = true })
	node.desired(t)
	states := answerBindChecks(node, func(check *agentpb.ValidateBindMounts) *agentpb.BindValidationResult {
		if check.Mounts[0].Source == "/srv/missing" {
			return &agentpb.BindValidationResult{Error: "technical-agent-secret /srv/missing"}
		}
		return &agentpb.BindValidationResult{Mounts: check.Mounts}
	})
	input := map[string]any{"name": "notified-app", "image": "nginx:alpine", "port": 80, "nodeId": node.nodeID, "bindMounts": []map[string]any{{"source": "/srv/media", "target": "/media"}}}
	var row bindRequestResponse
	postJSON(t, owner, base+"/api/bind-requests", input, &row)
	approve := base + "/api/admin/bind-requests/" + itoa(row.ID) + "/approve"
	if code := postJSON(t, superadmin, approve, struct{}{}, &row); code != http.StatusOK || row.Status != "created" {
		t.Fatalf("approval: %d %+v", code, row)
	}
	<-states
	for _, path := range []string{"/api/webhooks/1/review", "/api/webhooks/2/owner"} {
		hooks.waitTitle(t, path, "approuvée")
		hooks.waitTitle(t, path, "enregistrée")
	}
	if code := postJSON(t, superadmin, approve, struct{}{}, &row); code != http.StatusOK {
		t.Fatalf("repeat approval: %d", code)
	}
	input["name"] = "failed-notification-app"
	input["bindMounts"] = []map[string]any{{"source": "/srv/missing", "target": "/media"}}
	var failed bindRequestResponse
	postJSON(t, owner, base+"/api/bind-requests", input, &failed)
	if code := postJSON(t, superadmin, base+"/api/admin/bind-requests/"+itoa(failed.ID)+"/approve", struct{}{}, nil); code != http.StatusConflict {
		t.Fatalf("failed validation: %d", code)
	}
	for _, path := range []string{"/api/webhooks/1/review", "/api/webhooks/2/owner"} {
		hooks.waitTitle(t, path, "en échec")
	}
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	approved, created := 0, 0
	for _, post := range hooks.posts {
		raw, _ := json.Marshal(post)
		if strings.Contains(string(raw), "technical-agent-secret") || strings.Contains(string(raw), "/srv/") {
			t.Fatalf("technical data leaked: %s", raw)
		}
		title := post["embeds"].([]any)[0].(map[string]any)["title"].(string)
		if strings.Contains(title, "approuvée") {
			approved++
		}
		if strings.Contains(title, "enregistrée") {
			created++
		}
	}
	if approved != 2 || created != 2 {
		t.Fatalf("repeated approval or failed validation notified a new decision: approved=%d created=%d", approved, created)
	}
}
