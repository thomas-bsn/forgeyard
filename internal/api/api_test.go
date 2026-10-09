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
	"strings"
	"testing"
	"testing/fstest"

	"github.com/thomas-bsn/forgeyard/internal/nodes"
	"github.com/thomas-bsn/forgeyard/internal/pki"
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
	ca, err := pki.LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(Deps{
		Store: st, Logger: logger, Secrets: box, CA: ca, Nodes: nodes.NewHub(st, logger), AgentPort: "8081",
	}, testToken)
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

func put(t *testing.T, c *http.Client, url string, body any) int {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPut, url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
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
	// A password superadmin cannot turn password sign-in off.
	if code := put(t, c, ts.URL+"/api/admin/settings/login", loginSettings{PasswordLogin: false}); code != http.StatusConflict {
		t.Fatalf("password superadmin disabling password sign-in: %d", code)
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

func TestLoginLink(t *testing.T) {
	ts, server := newTestServerWithHandle(t)
	c := newClient()
	ctx := context.Background()

	if _, _, err := CreateSuperadminLoginLink(ctx, server.store); err == nil {
		t.Fatal("link created before setup")
	}
	post(t, c, ts.URL+"/api/setup", setupRequest{Token: testToken, InstanceName: "F", Username: "boss", Password: "a-long-enough-password"})
	post(t, c, ts.URL+"/api/auth/logout", struct{}{})

	link, user, err := CreateSuperadminLoginLink(ctx, server.store)
	if err != nil || user.Role != "superadmin" {
		t.Fatalf("create link: %v %+v", err, user)
	}
	// The link carries the address recorded at setup, which is the test server's.
	if !strings.HasPrefix(link, ts.URL+"/api/auth/link?token=") {
		t.Fatalf("link: %s", link)
	}
	resp, err := c.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	var me userResponse
	if code := get(t, c, ts.URL+"/api/auth/me", &me); code != http.StatusOK || me.Role != "superadmin" {
		t.Fatalf("me after link: %d %+v", code, me)
	}

	// A link works only once.
	other := newClient()
	resp, err = other.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Request.URL.Query().Get("link") != "invalid" {
		t.Fatalf("reused link landed on %s", resp.Request.URL)
	}
	if code := get(t, other, ts.URL+"/api/auth/me", nil); code != http.StatusUnauthorized {
		t.Fatalf("reused link signed in: %d", code)
	}
}
