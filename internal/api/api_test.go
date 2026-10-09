package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/thomas-bsn/forgeyard/internal/secrets"
	"github.com/thomas-bsn/forgeyard/internal/store"
)

const testToken = "setup-token"

func newTestServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	ts, _ := newTestServerWithHandle(t)
	jar, _ := cookiejar.New(nil)
	return ts, &http.Client{Jar: jar}
}

// newTestServerWithHandle also returns the Server, so tests can swap its dependencies.
func newTestServerWithHandle(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	webFS := fstest.MapFS{"index.html": {Data: []byte("<html></html>")}}
	box, err := secrets.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(st, logger, box, testToken)
	ts := httptest.NewServer(server.Handler(webFS))
	t.Cleanup(ts.Close)
	return ts, server
}

func post(t *testing.T, c *http.Client, url string, body any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := c.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func get(t *testing.T, c *http.Client, url string, out any) int {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func TestSetupAndLoginFlow(t *testing.T) {
	ts, c := newTestServer(t)

	var inst instanceResponse
	get(t, c, ts.URL+"/api/instance", &inst)
	if !inst.SetupRequired {
		t.Fatal("fresh instance should require setup")
	}

	setup := setupRequest{Token: testToken, InstanceName: "Ma Forge", Username: "Thomas", Password: "a-long-enough-password"}

	bad := setup
	bad.Token = "wrong"
	if code := post(t, c, ts.URL+"/api/setup", bad).StatusCode; code != http.StatusForbidden {
		t.Fatalf("wrong token: got %d", code)
	}
	weak := setup
	weak.Password = "short"
	if code := post(t, c, ts.URL+"/api/setup", weak).StatusCode; code != http.StatusBadRequest {
		t.Fatalf("weak password: got %d", code)
	}

	if code := post(t, c, ts.URL+"/api/setup", setup).StatusCode; code != http.StatusCreated {
		t.Fatalf("setup: got %d", code)
	}
	if code := post(t, c, ts.URL+"/api/setup", setup).StatusCode; code != http.StatusConflict {
		t.Fatalf("second setup: got %d", code)
	}

	var me userResponse
	if code := get(t, c, ts.URL+"/api/auth/me", &me); code != http.StatusOK || me.Role != "superadmin" || me.Username != "thomas" {
		t.Fatalf("me after setup: code=%d user=%+v", code, me)
	}
	get(t, c, ts.URL+"/api/instance", &inst)
	if inst.SetupRequired || inst.Name != "Ma Forge" {
		t.Fatalf("instance after setup: %+v", inst)
	}

	if code := post(t, c, ts.URL+"/api/auth/logout", struct{}{}).StatusCode; code != http.StatusNoContent {
		t.Fatalf("logout: got %d", code)
	}
	if code := get(t, c, ts.URL+"/api/auth/me", nil); code != http.StatusUnauthorized {
		t.Fatalf("me after logout: got %d", code)
	}

	if code := post(t, c, ts.URL+"/api/auth/login", loginRequest{"thomas", "nope-nope-nope"}).StatusCode; code != http.StatusUnauthorized {
		t.Fatalf("bad login: got %d", code)
	}
	if code := post(t, c, ts.URL+"/api/auth/login", loginRequest{"Thomas", "a-long-enough-password"}).StatusCode; code != http.StatusOK {
		t.Fatalf("login: got %d", code)
	}
	if code := get(t, c, ts.URL+"/api/auth/me", nil); code != http.StatusOK {
		t.Fatalf("me after login: got %d", code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	ts, c := newTestServer(t)
	for range 10 {
		post(t, c, ts.URL+"/api/auth/login", loginRequest{"ghost", "whatever-password"})
	}
	if code := post(t, c, ts.URL+"/api/auth/login", loginRequest{"ghost", "whatever-password"}).StatusCode; code != http.StatusTooManyRequests {
		t.Fatalf("expected rate limit, got %d", code)
	}
}

func TestWritesRequireJSON(t *testing.T) {
	ts, c := newTestServer(t)
	resp, err := c.Post(ts.URL+"/api/auth/login", "application/x-www-form-urlencoded", bytes.NewReader([]byte("username=a")))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("form post: got %d", resp.StatusCode)
	}
}
