package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/thomas-bsn/forgeyard/internal/discord"
)

// fakeDiscord approves every consent immediately, signing in as whichever user is set with as().
type fakeDiscord struct {
	*httptest.Server
	mu   sync.Mutex
	next discord.User
}

func newFakeDiscord(t *testing.T) *fakeDiscord {
	f := &fakeDiscord{}
	users := map[string]discord.User{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /authorize", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		u := f.next
		f.mu.Unlock()
		code := "code-" + u.ID
		f.mu.Lock()
		users[code] = u
		f.mu.Unlock()
		q := r.URL.Query()
		http.Redirect(w, r, q.Get("redirect_uri")+"?code="+code+"&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		if id, secret, _ := r.BasicAuth(); id != "client" || secret != "secret" {
			http.Error(w, "bad client", http.StatusUnauthorized)
			return
		}
		r.ParseForm()
		json.NewEncoder(w).Encode(map[string]string{"access_token": r.Form.Get("code")})
	})
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		u, ok := users[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		f.mu.Unlock()
		if !ok {
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(u)
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func (f *fakeDiscord) as(id, name string) {
	f.mu.Lock()
	f.next = discord.User{ID: id, Username: name, Email: name + "@example.com", Verified: true}
	f.mu.Unlock()
}

func newClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

// signIn runs the whole browser flow and returns the final page URL.
func signIn(t *testing.T, c *http.Client, ts *httptest.Server) *url.URL {
	t.Helper()
	resp, err := c.Get(ts.URL + "/api/auth/discord")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.Request.URL
}

func TestDiscordSetupRequestsAndLogin(t *testing.T) {
	ts, server := newTestServerWithHandle(t)
	fd := newFakeDiscord(t)
	server.discord = &discord.Client{HTTP: http.DefaultClient, Endpoints: discord.Endpoints{
		Authorize: fd.URL + "/authorize", Token: fd.URL + "/token", User: fd.URL + "/user",
	}}

	// Setup with Discord: the first account through the consent page becomes the superadmin.
	owner := newClient()
	fd.as("1", "thomas")
	b, _ := json.Marshal(setupDiscordRequest{Token: testToken, InstanceName: "Forge", ClientID: "client", ClientSecret: "secret"})
	resp, err := owner.Post(ts.URL+"/api/setup/discord", "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	var setup struct{ AuthorizeURL string }
	json.NewDecoder(resp.Body).Decode(&setup)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || setup.AuthorizeURL == "" {
		t.Fatalf("setup discord: code=%d", resp.StatusCode)
	}
	resp, err = owner.Get(setup.AuthorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	stored, err := server.store.GetSetting(context.Background(), settingDiscordClientSecret)
	if err != nil || strings.Contains(stored, "secret") {
		t.Fatalf("client secret stored in clear: %q %v", stored, err)
	}

	var me userResponse
	if code := get(t, owner, ts.URL+"/api/auth/me", &me); code != http.StatusOK || me.Role != "superadmin" || me.DisplayName != "thomas" {
		t.Fatalf("owner after setup: code=%d %+v", code, me)
	}

	// A Discord setup turns password sign-in off; Discord cannot then be turned off too.
	var inst instanceResponse
	get(t, owner, ts.URL+"/api/instance", &inst)
	if inst.PasswordLogin {
		t.Fatal("password sign-in should be off after a Discord setup")
	}
	if code := post(t, owner, ts.URL+"/api/auth/login", loginRequest{"x", "y"}).StatusCode; code != http.StatusForbidden {
		t.Fatalf("password login while disabled: %d", code)
	}
	if code := put(t, owner, ts.URL+"/api/admin/settings/discord", putDiscordSettings{}); code != http.StatusConflict {
		t.Fatalf("disabling the last sign-in method: %d", code)
	}
	if code := put(t, owner, ts.URL+"/api/admin/settings/login", loginSettings{PasswordLogin: true}); code != http.StatusOK {
		t.Fatalf("re-enabling password sign-in: %d", code)
	}
	get(t, owner, ts.URL+"/api/instance", &inst)
	if !inst.PasswordLogin {
		t.Fatal("password sign-in should be back on")
	}

	// An unknown Discord account gets a pending request, and keeps seeing it until an admin decides.
	lea := newClient()
	fd.as("2", "lea")
	if u := signIn(t, lea, ts); u.Query().Get("discord") != "pending" {
		t.Fatalf("lea first sign-in landed on %s", u)
	}
	if u := signIn(t, lea, ts); u.Query().Get("discord") != "pending" {
		t.Fatalf("lea second sign-in landed on %s", u)
	}
	var reqs []accountRequestResponse
	get(t, owner, ts.URL+"/api/admin/requests", &reqs)
	if len(reqs) != 1 || reqs[0].Username != "lea" {
		t.Fatalf("requests: %+v", reqs)
	}

	// Users cannot reach admin routes, and the superadmin role cannot be granted.
	if code := get(t, lea, ts.URL+"/api/admin/requests", nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous admin access: %d", code)
	}
	acceptURL := ts.URL + "/api/admin/requests/" + itoa(reqs[0].ID) + "/accept"
	if code := post(t, owner, acceptURL, acceptRequest{Role: "superadmin"}).StatusCode; code != http.StatusBadRequest {
		t.Fatalf("accept as superadmin: %d", code)
	}
	if code := post(t, owner, acceptURL, acceptRequest{Role: "user"}).StatusCode; code != http.StatusOK {
		t.Fatalf("accept: %d", code)
	}
	if u := signIn(t, lea, ts); u.Query().Get("discord") != "" {
		t.Fatalf("lea after acceptance landed on %s", u)
	}
	if code := get(t, lea, ts.URL+"/api/auth/me", &me); code != http.StatusOK || me.Role != "user" {
		t.Fatalf("lea me: code=%d %+v", code, me)
	}
	if code := get(t, lea, ts.URL+"/api/admin/requests", nil); code != http.StatusForbidden {
		t.Fatalf("user admin access: %d", code)
	}

	// A refused request stays refused.
	hugo := newClient()
	fd.as("3", "hugo")
	signIn(t, hugo, ts)
	get(t, owner, ts.URL+"/api/admin/requests", &reqs)
	if code := post(t, owner, ts.URL+"/api/admin/requests/"+itoa(reqs[0].ID)+"/refuse", refuseRequest{Reason: "non"}).StatusCode; code != http.StatusNoContent {
		t.Fatalf("refuse: %d", code)
	}
	if u := signIn(t, hugo, ts); u.Query().Get("discord") != "refused" {
		t.Fatalf("hugo after refusal landed on %s", u)
	}
}

func TestDiscordCallbackRejectsForeignState(t *testing.T) {
	ts, _ := newTestServer(t)
	c := newClient()
	resp, err := c.Get(ts.URL + "/api/auth/discord/callback?code=x&state=forged")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Request.URL.Query().Get("discord") != "expired" {
		t.Fatalf("forged state landed on %s", resp.Request.URL)
	}
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
