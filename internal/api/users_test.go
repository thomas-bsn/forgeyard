package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/auth"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

func del(t *testing.T, c *http.Client, url string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, url, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestUserManagement(t *testing.T) {
	ts, server := newTestServerWithHandle(t)
	admin := newClient()
	post(t, admin, ts.URL+"/api/setup", setupRequest{Token: testToken, InstanceName: "F", Username: "boss",
		Password: "a-long-enough-password", PublicURL: "https://forgeyard.example.com"})
	fa := connectFakeAgent(t, ts.URL, server, admin, "node-a")
	fa.desired(t)

	hash, _ := auth.HashPassword("another-long-password")
	if _, err := server.store.CreateLocalUser(context.Background(), db.CreateLocalUserParams{
		Username: nullString("lea"), PasswordHash: nullString(hash), DisplayName: "Léa", Role: "user", CreatedAt: time.Now().Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	lea := newClient()
	if resp := post(t, lea, ts.URL+"/api/auth/login", loginRequest{Username: "lea", Password: "another-long-password"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	var app appResponse
	if code := postJSON(t, lea, ts.URL+"/api/apps", appInput{Name: "shop", Image: "nginx", Port: 80}, &app); code != http.StatusCreated {
		t.Fatalf("create app: %d", code)
	}
	fa.desired(t)

	// The agent's reports become usage samples and events.
	for _, state := range []string{"creating", "running"} {
		fa.stream.Send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_AppStatuses{AppStatuses: &agentpb.AppStatuses{
			Apps: []*agentpb.AppStatus{{AppId: app.ID, State: state, CpuPercent: 12.5, MemoryUsedBytes: 64 << 20}},
		}}})
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var usage struct {
			Samples []struct {
				CPU float64 `json:"cpu"`
			} `json:"samples"`
		}
		get(t, lea, ts.URL+"/api/apps/"+itoa(app.ID)+"/usage", &usage)
		var events []appEventResponse
		get(t, lea, ts.URL+"/api/apps/"+itoa(app.ID)+"/events", &events)
		if len(usage.Samples) == 1 && usage.Samples[0].CPU == 12.5 && len(events) > 0 && events[0].Message == "En ligne" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("usage %+v, events %+v", usage, events)
		}
		time.Sleep(50 * time.Millisecond)
	}

	var users []accountResponse
	get(t, admin, ts.URL+"/api/admin/users", &users)
	if len(users) != 2 || users[0].Role != "superadmin" || users[1].AppCount != 1 || users[1].Method != "password" {
		t.Fatalf("users: %+v", users)
	}
	if code := get(t, lea, ts.URL+"/api/admin/users", nil); code != http.StatusForbidden {
		t.Fatalf("users listed by a user: %d", code)
	}
	if code := put(t, admin, ts.URL+"/api/admin/users/"+itoa(users[0].ID), updateUserRequest{Disabled: ptr(true)}); code != http.StatusForbidden {
		t.Fatalf("superadmin changed: %d", code)
	}

	// Suspending stops the apps and keeps them stopped.
	if code := put(t, admin, ts.URL+"/api/admin/users/"+itoa(app.OwnerID), updateUserRequest{AppsSuspended: ptr(true)}); code != http.StatusNoContent {
		t.Fatalf("suspend: %d", code)
	}
	if s := fa.desired(t).GetApps()[0]; s.GetRunning() {
		t.Fatal("suspended app still running")
	}
	if code := postJSON(t, lea, ts.URL+"/api/apps/"+itoa(app.ID)+"/start", struct{}{}, nil); code != http.StatusForbidden {
		t.Fatalf("start while suspended: %d", code)
	}
	if code := postJSON(t, admin, ts.URL+"/api/apps/"+itoa(app.ID)+"/start", struct{}{}, nil); code != http.StatusForbidden {
		t.Fatalf("admin start while suspended: %d", code)
	}
	put(t, admin, ts.URL+"/api/admin/users/"+itoa(app.OwnerID), updateUserRequest{AppsSuspended: ptr(false), Role: ptr("admin")})
	if code := postJSON(t, lea, ts.URL+"/api/apps/"+itoa(app.ID)+"/start", struct{}{}, nil); code != http.StatusOK {
		t.Fatalf("start after lifting: %d", code)
	}
	fa.desired(t)

	// Disabling signs the user out; deleting removes their apps.
	put(t, admin, ts.URL+"/api/admin/users/"+itoa(app.OwnerID), updateUserRequest{Disabled: ptr(true)})
	if code := get(t, lea, ts.URL+"/api/apps", nil); code != http.StatusUnauthorized {
		t.Fatalf("disabled user still signed in: %d", code)
	}
	if code := del(t, admin, ts.URL+"/api/admin/users/"+itoa(app.OwnerID)); code != http.StatusNoContent {
		t.Fatalf("delete user: %d", code)
	}
	if len(fa.desired(t).GetApps()) != 0 {
		t.Fatal("app of a deleted user still desired")
	}
	get(t, admin, ts.URL+"/api/admin/users", &users)
	if len(users) != 1 {
		t.Fatalf("users after deletion: %+v", users)
	}

	// General settings.
	if code := put(t, admin, ts.URL+"/api/admin/settings/general", generalSettings{Name: "Forge", PublicURL: "https://f.example.com/"}); code != http.StatusOK {
		t.Fatalf("general settings: %d", code)
	}
	var inst instanceResponse
	get(t, admin, ts.URL+"/api/instance", &inst)
	if inst.Name != "Forge" {
		t.Fatalf("instance name: %q", inst.Name)
	}
}

func ptr[T any](v T) *T { return &v }

func TestExternalContainers(t *testing.T) {
	ts, server := newTestServerWithHandle(t)
	admin := newClient()
	post(t, admin, ts.URL+"/api/setup", setupRequest{Token: testToken, InstanceName: "F", Username: "boss",
		Password: "a-long-enough-password", PublicURL: "https://forgeyard.example.com"})
	fa := connectFakeAgent(t, ts.URL, server, admin, "node-a")
	fa.desired(t)

	fa.stream.Send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_ExternalContainers{ExternalContainers: &agentpb.ExternalContainers{
		Containers: []*agentpb.ExternalContainer{{Id: "abc123", Name: "caddy", Image: "caddy:2", State: "running", Ports: []string{"443->443/tcp"}}},
	}}})
	var list []containerResponse
	deadline := time.Now().Add(5 * time.Second)
	for len(list) == 0 {
		get(t, admin, ts.URL+"/api/admin/containers", &list)
		if time.Now().After(deadline) {
			t.Fatal("external containers never listed")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if c := list[0]; c.Name != "caddy" || c.NodeName != "node-a" || c.OwnerName != "boss" {
		t.Fatalf("container: %+v", c)
	}

	base := ts.URL + "/api/admin/nodes/" + itoa(fa.nodeID) + "/containers/"
	if code := postJSON(t, admin, base+"unknown/stop", struct{}{}, nil); code != http.StatusNotFound {
		t.Fatalf("unknown container: %d", code)
	}
	if code := postJSON(t, admin, base+"abc123/restart", struct{}{}, nil); code != http.StatusAccepted {
		t.Fatalf("restart: %d", code)
	}
	for {
		msg, err := fa.stream.Recv()
		if err != nil {
			t.Fatal(err)
		}
		if a := msg.GetContainerAction(); a != nil {
			if a.GetContainerId() != "abc123" || a.GetAction() != "restart" {
				t.Fatalf("action: %v", a)
			}
			break
		}
	}
}
