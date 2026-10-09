package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thomas-bsn/forgeyard/internal/cloudflare"
)

func postJSON(t *testing.T, c *http.Client, url string, body, out any) int {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := c.Post(url, "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func TestDomainSettings(t *testing.T) {
	ts, server := newTestServerWithHandle(t)
	admin := newClient()
	post(t, admin, ts.URL+"/api/setup", setupRequest{Token: testToken, InstanceName: "F", Username: "boss", Password: "a-long-enough-password", PublicURL: "https://forge.example.com/"})

	var d domainSettings
	get(t, admin, ts.URL+"/api/admin/settings/domain", &d)
	if d.PublicURL != "https://forge.example.com" || d.Mode != "none" || d.DiscordRedirectURL != "https://forge.example.com/api/auth/discord/callback" {
		t.Fatalf("after setup: %+v", d)
	}

	for _, bad := range []putDomainSettings{
		{PublicURL: "forge.example.com", Mode: "none"},
		{PublicURL: "https://forge.example.com/path", Mode: "none"},
		{PublicURL: "https://forge.example.com", Mode: "wildcard", Domain: "not a domain", PublicIP: "1.2.3.4"},
		{PublicURL: "https://forge.example.com", Mode: "wildcard", Domain: "example.com", PublicIP: "nope"},
		{PublicURL: "https://forge.example.com", Mode: "magic"},
	} {
		if code := put(t, admin, ts.URL+"/api/admin/settings/domain", bad); code != http.StatusBadRequest {
			t.Fatalf("%+v accepted: %d", bad, code)
		}
	}

	// Wildcard: the check resolves a random subdomain.
	server.lookupHost = func(_ context.Context, host string) ([]string, error) {
		if strings.HasSuffix(host, ".example.com") {
			return []string{"1.2.3.4"}, nil
		}
		return nil, errors.New("no such host")
	}
	if code := put(t, admin, ts.URL+"/api/admin/settings/domain", putDomainSettings{
		PublicURL: "https://forge.example.com", Mode: "wildcard", Domain: "Example.com", PublicIP: "1.2.3.4",
	}); code != http.StatusOK {
		t.Fatalf("wildcard save: %d", code)
	}
	var check domainCheckResponse
	postJSON(t, admin, ts.URL+"/api/admin/settings/domain/check", struct{}{}, &check)
	if !check.OK || !strings.HasPrefix(check.Name, "forgeyard-check-") {
		t.Fatalf("wildcard check: %+v", check)
	}
	put(t, admin, ts.URL+"/api/admin/settings/domain", putDomainSettings{
		PublicURL: "https://forge.example.com", Mode: "wildcard", Domain: "example.com", PublicIP: "5.6.7.8",
	})
	postJSON(t, admin, ts.URL+"/api/admin/settings/domain/check", struct{}{}, &check)
	if check.OK {
		t.Fatalf("wildcard pointing elsewhere passed: %+v", check)
	}

	// Cloudflare: the token and zone are checked before saving, and the token is stored encrypted.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /user/tokens/verify", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer cf-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]string{"status": "active"}})
	})
	mux.HandleFunc("GET /zones", func(w http.ResponseWriter, r *http.Request) {
		zones := []cloudflare.Zone{}
		if r.URL.Query().Get("name") == "example.com" {
			zones = append(zones, cloudflare.Zone{ID: "zone-1", Name: "example.com"})
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": zones})
	})
	cf := httptest.NewServer(mux)
	defer cf.Close()
	server.newCloudflare = func(token string) *cloudflare.Client {
		return &cloudflare.Client{BaseURL: cf.URL, Token: token, HTTP: cf.Client()}
	}

	if code := put(t, admin, ts.URL+"/api/admin/settings/domain", putDomainSettings{
		PublicURL: "https://forge.example.com", Mode: "cloudflare", Domain: "apps.example.com", PublicIP: "1.2.3.4", CloudflareToken: "wrong",
	}); code != http.StatusBadRequest {
		t.Fatalf("bad cloudflare token accepted: %d", code)
	}
	if code := put(t, admin, ts.URL+"/api/admin/settings/domain", putDomainSettings{
		PublicURL: "https://forge.example.com", Mode: "cloudflare", Domain: "apps.example.com", PublicIP: "1.2.3.4", CloudflareToken: "cf-token",
	}); code != http.StatusOK {
		t.Fatalf("cloudflare save: %d", code)
	}
	get(t, admin, ts.URL+"/api/admin/settings/domain", &d)
	if !d.CloudflareHasToken || d.CloudflareZone != "example.com" {
		t.Fatalf("cloudflare settings: %+v", d)
	}
	stored, _ := server.store.GetSetting(context.Background(), settingCloudflareToken)
	if strings.Contains(stored, "cf-token") {
		t.Fatal("cloudflare token stored in clear")
	}
	// Saving again without a token keeps the stored one.
	if code := put(t, admin, ts.URL+"/api/admin/settings/domain", putDomainSettings{
		PublicURL: "https://forge.example.com", Mode: "cloudflare", Domain: "apps.example.com", PublicIP: "1.2.3.4",
	}); code != http.StatusOK {
		t.Fatalf("cloudflare save without token: %d", code)
	}
	postJSON(t, admin, ts.URL+"/api/admin/settings/domain/check", struct{}{}, &check)
	if !check.OK {
		t.Fatalf("cloudflare check: %+v", check)
	}
}

func TestDiscordSetupRequiresPublicOrigin(t *testing.T) {
	ts, _ := newTestServer(t)
	c := newClient()
	var e errorResponse
	code := postJSON(t, c, ts.URL+"/api/setup/discord", setupDiscordRequest{
		Token: testToken, InstanceName: "F", PublicURL: "https://elsewhere.example.com", ClientID: "c", ClientSecret: "s",
	}, &e)
	if code != http.StatusBadRequest || !strings.Contains(e.Error, "elsewhere.example.com") {
		t.Fatalf("discord setup from another origin: %d %q", code, e.Error)
	}
}
