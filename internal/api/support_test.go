package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/auth"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

func TestSupport(t *testing.T) {
	ts, server := newTestServerWithHandle(t)
	admin := newClient()
	post(t, admin, ts.URL+"/api/setup", setupRequest{Token: testToken, InstanceName: "F", Username: "boss",
		Password: "a-long-enough-password", PublicURL: "https://forgeyard.example.com"})
	hooks := &fakeWebhooks{}
	hooks.install(t, server)
	// Support goes to its own channel; the admins' channel keeps the rest.
	if code := putJSON(t, admin, ts.URL+"/api/admin/settings/notifications", notifySettings{
		Webhook: "https://discord.com/api/webhooks/1/admin", SupportWebhook: "https://discord.com/api/webhooks/3/support",
		Events: map[string]bool{"support": true, "requests": true},
	}, nil); code != http.StatusOK {
		t.Fatalf("notification settings: %d", code)
	}
	var settings notifySettings
	get(t, admin, ts.URL+"/api/admin/settings/notifications", &settings)
	if !settings.SupportWebhookSet || !settings.Events["support"] || settings.Events["crashes"] {
		t.Fatalf("settings: %+v", settings)
	}

	newUser := func(name string) *http.Client {
		hash, _ := auth.HashPassword("another-long-password")
		if _, err := server.store.CreateLocalUser(context.Background(), db.CreateLocalUserParams{
			Username: nullString(name), PasswordHash: nullString(hash), DisplayName: name, Role: "user", CreatedAt: time.Now().Unix(),
		}); err != nil {
			t.Fatal(err)
		}
		c := newClient()
		if resp := post(t, c, ts.URL+"/api/auth/login", loginRequest{Username: name, Password: "another-long-password"}); resp.StatusCode != http.StatusOK {
			t.Fatalf("login: %d", resp.StatusCode)
		}
		return c
	}
	lea, max := newUser("lea"), newUser("max")
	putJSON(t, lea, ts.URL+"/api/me/notifications", map[string]string{"webhook": "https://discord.com/api/webhooks/2/lea"}, nil)

	for _, bad := range []ticketRequest{
		{Kind: "other", Subject: "Bonjour", Body: "x"},
		{Kind: "general", Subject: "Hi", Body: "x"},
		{Kind: "general", Subject: "Bonjour", Body: "  "},
		{Kind: "app", AppID: 999, Subject: "Mon app", Body: "x"},
	} {
		if code := postJSON(t, lea, ts.URL+"/api/support/tickets", bad, nil); code != http.StatusBadRequest {
			t.Fatalf("%+v: %d", bad, code)
		}
	}
	var ticket ticketDetail
	if code := postJSON(t, lea, ts.URL+"/api/support/tickets", ticketRequest{Kind: "infra", Subject: "  Le   Pi est lent ", Body: "Depuis ce matin."}, &ticket); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	if ticket.Subject != "Le Pi est lent" || !ticket.Waiting || ticket.Status != "open" || len(ticket.Thread) != 1 || ticket.Thread[0].Staff {
		t.Fatalf("ticket: %+v", ticket)
	}
	hooks.waitTitle(t, "/api/webhooks/3/support", "Le Pi est lent")

	// Only its author and the admins see it.
	if code := get(t, max, ts.URL+"/api/support/tickets/"+itoa(ticket.ID), nil); code != http.StatusNotFound {
		t.Fatalf("another user: %d", code)
	}
	var mine []ticketResponse
	get(t, max, ts.URL+"/api/support/tickets", &mine)
	if len(mine) != 0 {
		t.Fatalf("another user's list: %+v", mine)
	}

	// An admin answers: the author hears about it, and the request no longer waits.
	var answered ticketDetail
	if code := postJSON(t, admin, ts.URL+"/api/support/tickets/"+itoa(ticket.ID)+"/messages", map[string]string{"body": "Je regarde."}, &answered); code != http.StatusOK {
		t.Fatalf("answer: %d", code)
	}
	if answered.Waiting || len(answered.Thread) != 2 || !answered.Thread[1].Staff {
		t.Fatalf("answered: %+v", answered)
	}
	hooks.waitTitle(t, "/api/webhooks/2/lea", "Réponse du support")

	// Closed, then reopened by a new message from its author.
	var closed ticketDetail
	putJSON(t, lea, ts.URL+"/api/support/tickets/"+itoa(ticket.ID)+"/status", map[string]string{"status": "closed"}, &closed)
	if closed.Status != "closed" {
		t.Fatalf("closed: %+v", closed)
	}
	var reopened ticketDetail
	postJSON(t, lea, ts.URL+"/api/support/tickets/"+itoa(ticket.ID)+"/messages", map[string]string{"body": "Toujours lent."}, &reopened)
	if reopened.Status != "open" || !reopened.Waiting {
		t.Fatalf("reopened: %+v", reopened)
	}
	var all []ticketResponse
	get(t, admin, ts.URL+"/api/support/tickets", &all)
	if len(all) != 1 || all[0].AuthorName != "lea" || all[0].Messages != 3 {
		t.Fatalf("admin list: %+v", all)
	}
}
