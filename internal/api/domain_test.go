package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/thomas-bsn/forgeyard/internal/dns"
	"github.com/thomas-bsn/forgeyard/internal/dns/dnstest"
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

	// Provider: the credentials and zone are checked before saving, and stored encrypted.
	mem := dnstest.New("example.com.")
	server.newDNSProvider = func(name string, creds map[string]string) (dns.Provider, error) {
		if creds["api_token"] != "cf-token" {
			return nil, errors.New("bad token")
		}
		return mem, nil
	}
	bad := putDomainSettings{PublicURL: "https://forge.example.com", Mode: "provider", Domain: "apps.example.com",
		PublicIP: "1.2.3.4", Provider: "cloudflare", Credentials: map[string]string{"api_token": "wrong"}}
	if code := put(t, admin, ts.URL+"/api/admin/settings/domain", bad); code != http.StatusBadRequest {
		t.Fatalf("bad credentials accepted: %d", code)
	}
	good := bad
	good.Credentials = map[string]string{"api_token": "cf-token"}
	if code := put(t, admin, ts.URL+"/api/admin/settings/domain", good); code != http.StatusOK {
		t.Fatalf("provider save: %d", code)
	}
	get(t, admin, ts.URL+"/api/admin/settings/domain", &d)
	if d.Provider != "cloudflare" || d.Zone != "example.com" || len(d.SecretsSet) != 1 || d.Credentials["api_token"] != "" {
		t.Fatalf("provider settings: %+v", d)
	}
	stored, _ := server.store.GetSetting(context.Background(), settingDNSCredentials)
	if strings.Contains(stored, "cf-token") {
		t.Fatal("DNS credentials stored in clear")
	}
	// Saving again with the secret left empty keeps the stored one.
	good.Credentials = map[string]string{}
	if code := put(t, admin, ts.URL+"/api/admin/settings/domain", good); code != http.StatusOK {
		t.Fatalf("provider save without secret: %d", code)
	}
	postJSON(t, admin, ts.URL+"/api/admin/settings/domain/check", struct{}{}, &check)
	if !check.OK {
		t.Fatalf("provider check: %+v", check)
	}
	// A zone the credentials cannot reach is refused.
	good.Domain = "other.org"
	if code := put(t, admin, ts.URL+"/api/admin/settings/domain", good); code != http.StatusBadRequest {
		t.Fatalf("unreachable zone accepted: %d", code)
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
