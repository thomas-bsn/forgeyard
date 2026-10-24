package api

import (
	"context"
	"github.com/thomas-bsn/forgeyard/internal/auth"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
	"net/http"
	"testing"
	"time"
)

func TestBindRequestSubmission(t *testing.T) {
	base, _, admin, node := setupStackServer(t)
	input := map[string]any{"name": "media", "image": "nginx:alpine", "port": 80, "memoryMb": 128, "env": map[string]string{"TOKEN": "private-value"}, "nodeId": node.nodeID, "bindMounts": []map[string]any{{"source": "/srv/media", "target": "/media"}}}
	var submitted map[string]any
	if code := postJSON(t, admin, base+"/api/bind-requests", input, &submitted); code != http.StatusCreated {
		t.Fatalf("submit request: %d", code)
	}
	id := int64(submitted["id"].(float64))
	if submitted["status"] != "pending" {
		t.Fatalf("submission: %+v", submitted)
	}
	var requests []map[string]any
	if code := get(t, admin, base+"/api/bind-requests", &requests); code != http.StatusOK || len(requests) != 1 {
		t.Fatalf("requests: %d %+v", code, requests)
	}
	var apps []appResponse
	get(t, admin, base+"/api/apps", &apps)
	if len(apps) != 0 {
		t.Fatalf("submission created apps: %+v", apps)
	}
	var stored map[string]any
	get(t, admin, base+"/api/bind-requests/"+itoa(id), &stored)
	config := stored["config"].(map[string]any)
	if config["env"] != nil {
		t.Fatalf("ordinary response reveals env: %+v", config)
	}
	mounts := config["bindMounts"].([]any)
	if mounts[0].(map[string]any)["readOnly"] != true {
		t.Fatalf("read-only must be the default: %+v", mounts)
	}
	var revealed map[string]string
	if code := postJSON(t, admin, base+"/api/admin/bind-requests/"+itoa(id)+"/reveal", struct{}{}, &revealed); code != http.StatusOK || revealed["TOKEN"] != "private-value" {
		t.Fatalf("reveal: %d %+v", code, revealed)
	}
	// A pending request reserves no name, including against another pending request.
	if code := postJSON(t, admin, base+"/api/bind-requests", input, nil); code != http.StatusCreated {
		t.Fatalf("same name: %d", code)
	}
	// There is no endpoint to mutate a submitted configuration or approve in this slice.
	if code := putJSON(t, admin, base+"/api/bind-requests/"+itoa(id), input, nil); code != http.StatusNotFound {
		t.Fatalf("immutable request: %d", code)
	}
	if code := postJSON(t, admin, base+"/api/admin/bind-requests/"+itoa(id)+"/approve", struct{}{}, nil); code != http.StatusNotFound {
		t.Fatalf("premature approve: %d", code)
	}
	delete(input, "bindMounts")
	if code := postJSON(t, admin, base+"/api/apps", input, nil); code != http.StatusCreated {
		t.Fatalf("pending request reserved the app name: %d", code)
	}
}

func TestBindRequestPermissionsAndDecisions(t *testing.T) {
	base, server, superadmin, node := setupStackServer(t)
	newUser := func(name, role string) *http.Client {
		hash, err := auth.HashPassword("another-long-password")
		if err != nil {
			t.Fatal(err)
		}
		_, err = server.store.CreateLocalUser(context.Background(), db.CreateLocalUserParams{Username: nullString(name), PasswordHash: nullString(hash), DisplayName: name, Role: role, CreatedAt: time.Now().Unix()})
		if err != nil {
			t.Fatal(err)
		}
		c := newClient()
		if code := post(t, c, base+"/api/auth/login", loginRequest{Username: name, Password: "another-long-password"}).StatusCode; code != http.StatusOK {
			t.Fatalf("login %s: %d", name, code)
		}
		return c
	}
	owner, other, admin := newUser("lea", "user"), newUser("max", "user"), newUser("admin", "admin")
	input := map[string]any{"name": "gallery", "image": "nginx:alpine", "port": 80, "nodeId": node.nodeID, "env": map[string]string{"TOKEN": "secret"}, "bindMounts": []map[string]any{{"source": "/srv/photos", "target": "/photos"}}}
	var row map[string]any
	if code := postJSON(t, owner, base+"/api/bind-requests", input, &row); code != http.StatusCreated {
		t.Fatalf("submit: %d", code)
	}
	id := int64(row["id"].(float64))
	url := base + "/api/bind-requests/" + itoa(id)
	staff := base + "/api/admin/bind-requests"
	for _, c := range []*http.Client{other, admin} {
		if code := get(t, c, url, nil); code != http.StatusNotFound {
			t.Fatalf("other user detail: %d", code)
		}
		if code := get(t, c, staff, nil); code != http.StatusForbidden {
			t.Fatalf("staff queue: %d", code)
		}
		for _, action := range []string{"refuse", "reveal"} {
			if code := postJSON(t, c, staff+"/"+itoa(id)+"/"+action, map[string]string{"reason": "no"}, nil); code != http.StatusForbidden {
				t.Fatalf("staff %s: %d", action, code)
			}
		}
	}
	if code := postJSON(t, owner, staff+"/"+itoa(id)+"/reveal", struct{}{}, nil); code != http.StatusForbidden {
		t.Fatalf("owner reveal: %d", code)
	}
	var mine []map[string]any
	get(t, other, base+"/api/bind-requests", &mine)
	if len(mine) != 0 {
		t.Fatalf("other's list: %+v", mine)
	}
	var queue []map[string]any
	if code := get(t, superadmin, staff, &queue); code != http.StatusOK || len(queue) != 1 {
		t.Fatalf("superadmin queue: %d %+v", code, queue)
	}
	if code := postJSON(t, superadmin, staff+"/"+itoa(id)+"/refuse", map[string]string{"reason": "  "}, nil); code != http.StatusBadRequest {
		t.Fatalf("blank reason: %d", code)
	}
	var refused map[string]any
	if code := postJSON(t, superadmin, staff+"/"+itoa(id)+"/refuse", map[string]string{"reason": " Données non autorisées "}, &refused); code != http.StatusOK || refused["status"] != "refused" || refused["reason"] != "Données non autorisées" {
		t.Fatalf("refusal: %d %+v", code, refused)
	}
	if code := postJSON(t, owner, url+"/cancel", struct{}{}, nil); code != http.StatusConflict {
		t.Fatalf("cancel refused: %d", code)
	}
	var next map[string]any
	postJSON(t, owner, base+"/api/bind-requests", input, &next)
	nextID := int64(next["id"].(float64))
	if code := postJSON(t, owner, base+"/api/bind-requests/"+itoa(nextID)+"/cancel", struct{}{}, &next); code != http.StatusOK || next["status"] != "cancelled" {
		t.Fatalf("cancel: %d %+v", code, next)
	}
	var choices []map[string]any
	if code := get(t, owner, base+"/api/bind-requests/nodes", &choices); code != http.StatusOK || len(choices) != 1 || choices[0]["online"] != true {
		t.Fatalf("node choices: %d %+v", code, choices)
	}
	// Unknown fields cannot turn this submission into an approval or modify a request's status.
	input["status"] = "approved"
	if code := postJSON(t, owner, base+"/api/bind-requests", input, nil); code != http.StatusBadRequest {
		t.Fatalf("status injection: %d", code)
	}
}
