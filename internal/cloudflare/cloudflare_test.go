package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func fakeAPI(t *testing.T) *Client {
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, result any) {
		json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": result})
	}
	mux.HandleFunc("GET /user/tokens/verify", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		reply(w, map[string]string{"status": "active"})
	})
	mux.HandleFunc("GET /zones", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") == "example.com" {
			reply(w, []Zone{{ID: "z1", Name: "example.com"}})
			return
		}
		reply(w, []Zone{})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL, Token: "good", HTTP: srv.Client()}
}

func TestVerifyAndFindZone(t *testing.T) {
	c := fakeAPI(t)
	if err := c.VerifyToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	zone, err := c.FindZone(context.Background(), "apps.Example.com.")
	if err != nil || zone.ID != "z1" {
		t.Fatalf("zone: %+v %v", zone, err)
	}
	if _, err := c.FindZone(context.Background(), "other.org"); err == nil {
		t.Fatal("found a zone for an unknown domain")
	}
	c.Token = "bad"
	if err := c.VerifyToken(context.Background()); err != ErrInvalidToken {
		t.Fatalf("bad token: %v", err)
	}
}
