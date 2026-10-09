package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/dns"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

const (
	settingAppsDomainMode = "apps_domain_mode" // none, wildcard or provider
	settingAppsDomain     = "apps_domain"
	settingAppsPublicIP   = "apps_public_ip"
	settingDNSProvider    = "dns_provider"
	settingDNSCredentials = "dns_credentials" // encrypted JSON object
	settingDNSZone        = "dns_zone"

	// Before several providers were supported, Cloudflare was a mode of its own.
	legacyCloudflareToken = "cloudflare_api_token"
)

var domainPattern = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// normalizePublicURL keeps the scheme, host and port of the address Forgeyard is reached at.
func normalizePublicURL(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	return u.Scheme + "://" + strings.ToLower(u.Host), true
}

func (s *Server) setting(ctx context.Context, q *db.Queries, key string) (string, error) {
	v, err := q.GetSetting(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// dnsConfig is the saved DNS provider configuration, credentials decrypted.
type dnsConfig struct {
	Mode     string
	Domain   string
	PublicIP string
	Provider string
	Zone     string
	Creds    map[string]string
}

func (s *Server) loadDNSConfig(ctx context.Context) (dnsConfig, error) {
	var c dnsConfig
	q := s.store.Queries
	var err error
	for key, dst := range map[string]*string{
		settingAppsDomainMode: &c.Mode, settingAppsDomain: &c.Domain, settingAppsPublicIP: &c.PublicIP,
		settingDNSProvider: &c.Provider, settingDNSZone: &c.Zone,
	} {
		if *dst, err = s.setting(ctx, q, key); err != nil {
			return c, err
		}
	}
	if c.Mode == "" {
		c.Mode = "none"
	}
	sealed, err := s.setting(ctx, q, settingDNSCredentials)
	if err != nil {
		return c, err
	}
	if sealed != "" {
		plain, err := s.secrets.Decrypt(sealed, settingDNSCredentials)
		if err != nil {
			return c, err
		}
		if err := json.Unmarshal([]byte(plain), &c.Creds); err != nil {
			return c, err
		}
	}
	if c.Mode == "cloudflare" { // saved before several providers were supported
		c.Mode, c.Provider = "provider", "cloudflare"
		if c.Creds == nil {
			if sealed, err := s.setting(ctx, q, legacyCloudflareToken); err != nil {
				return c, err
			} else if sealed != "" {
				token, err := s.secrets.Decrypt(sealed, legacyCloudflareToken)
				if err != nil {
					return c, err
				}
				c.Creds = map[string]string{"api_token": token}
			}
		}
	}
	return c, nil
}

type domainSettings struct {
	PublicURL          string            `json:"publicUrl"`
	DiscordRedirectURL string            `json:"discordRedirectUrl"`
	Mode               string            `json:"mode"`
	Domain             string            `json:"domain"`
	PublicIP           string            `json:"publicIp"`
	Provider           string            `json:"provider"`
	Zone               string            `json:"zone,omitempty"`
	Credentials        map[string]string `json:"credentials"` // non-secret fields only
	SecretsSet         []string          `json:"secretsSet"`  // secret fields that have a stored value
	Providers          []dns.Kind        `json:"providers"`
	// Warning reports apps the last save could not update (e.g. a DNS record the provider refused).
	Warning string `json:"warning,omitempty"`
}

func (s *Server) loadDomainSettings(ctx context.Context, r *http.Request) (domainSettings, error) {
	d := domainSettings{Credentials: map[string]string{}, SecretsSet: []string{}, Providers: dns.Kinds}
	var err error
	if d.PublicURL, err = s.publicURL(ctx, r); err != nil {
		return d, err
	}
	if d.DiscordRedirectURL, err = s.discordRedirectURL(ctx, r); err != nil {
		return d, err
	}
	c, err := s.loadDNSConfig(ctx)
	if err != nil {
		return d, err
	}
	d.Mode, d.Domain, d.PublicIP, d.Provider, d.Zone = c.Mode, c.Domain, c.PublicIP, c.Provider, strings.TrimSuffix(c.Zone, ".")
	// Secrets never leave the server: the UI only learns which ones are set.
	if kind, ok := dns.Lookup(c.Provider); ok {
		for _, f := range kind.Fields {
			if c.Creds[f.Key] == "" {
				continue
			}
			if f.Secret {
				d.SecretsSet = append(d.SecretsSet, f.Key)
			} else {
				d.Credentials[f.Key] = c.Creds[f.Key]
			}
		}
	}
	return d, nil
}

func (s *Server) handleGetDomainSettings(w http.ResponseWriter, r *http.Request) {
	d, err := s.loadDomainSettings(r.Context(), r)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

type putDomainSettings struct {
	PublicURL   string            `json:"publicUrl"`
	Mode        string            `json:"mode"`
	Domain      string            `json:"domain"`
	PublicIP    string            `json:"publicIp"`
	Provider    string            `json:"provider"`
	Credentials map[string]string `json:"credentials"` // an empty secret keeps the stored value
}

// handlePutDomainSettings saves Forgeyard's address and how app domains are handled. With a DNS provider,
// the credentials and the zone are checked with the provider before anything is saved.
func (s *Server) handlePutDomainSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body putDomainSettings
	if !decodeJSON(w, r, &body) {
		return
	}
	publicURL, ok := normalizePublicURL(body.PublicURL)
	if !ok {
		writeError(w, http.StatusBadRequest, "adresse de Forgeyard invalide : par exemple https://forgeyard.mondomaine.com")
		return
	}
	domain := strings.Trim(strings.ToLower(strings.TrimSpace(body.Domain)), ".")
	ip := strings.TrimSpace(body.PublicIP)
	switch body.Mode {
	case "none":
	case "wildcard", "provider":
		if !domainPattern.MatchString(domain) {
			writeError(w, http.StatusBadRequest, "domaine invalide : par exemple mondomaine.com")
			return
		}
		if net.ParseIP(ip) == nil {
			writeError(w, http.StatusBadRequest, "IP publique invalide : l'adresse vers laquelle pointent les apps")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "mode inconnu")
		return
	}

	values := map[string]string{
		settingPublicURL: publicURL, settingAppsDomainMode: body.Mode, settingAppsDomain: domain, settingAppsPublicIP: ip,
	}
	if body.Mode == "provider" {
		kind, ok := dns.Lookup(body.Provider)
		if !ok {
			writeError(w, http.StatusBadRequest, "fournisseur DNS inconnu")
			return
		}
		saved, err := s.loadDNSConfig(ctx)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		creds := map[string]string{}
		for _, f := range kind.Fields {
			v := strings.TrimSpace(body.Credentials[f.Key])
			if v == "" && f.Secret && saved.Provider == kind.Name {
				v = saved.Creds[f.Key] // left empty in the form: keep the stored secret
			}
			creds[f.Key] = v
		}
		provider, err := s.newDNSProvider(kind.Name, creds)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		zone, err := dns.FindZone(checkCtx, provider, domain)
		if err != nil {
			writeError(w, http.StatusBadRequest, kind.Label+" : "+err.Error())
			return
		}
		raw, _ := json.Marshal(creds)
		sealed, err := s.secrets.Encrypt(string(raw), settingDNSCredentials)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		values[settingDNSProvider] = kind.Name
		values[settingDNSCredentials] = sealed
		values[settingDNSZone] = zone
	}

	err := s.store.InTx(ctx, func(q *db.Queries) error {
		for k, v := range values {
			if err := q.SetSetting(ctx, db.SetSettingParams{Key: k, Value: v}); err != nil {
				return err
			}
		}
		return q.DeleteSetting(ctx, legacyCloudflareToken)
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.logger.Info("domain settings changed", "public_url", publicURL, "mode", body.Mode, "domain", domain,
		"provider", values[settingDNSProvider], "by", currentUser(r).DisplayName)

	// Existing apps follow: new records, new domains routed by their nodes.
	d, err := s.loadDomainSettings(ctx, r)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.syncApps(ctx); err != nil {
		s.logger.Warn("updating apps after a domain change failed", "err", err)
		d.Warning = err.Error()
	}
	writeJSON(w, http.StatusOK, d)
}

type domainCheckResponse struct {
	OK       bool     `json:"ok"`
	Message  string   `json:"message"`
	Name     string   `json:"name,omitempty"`
	Resolved []string `json:"resolved,omitempty"`
}

// handleCheckDomain tests the saved configuration: in wildcard mode, that a random subdomain resolves to the
// public IP; with a provider, that the credentials still reach the zone.
func (s *Server) handleCheckDomain(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	c, err := s.loadDNSConfig(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	switch c.Mode {
	case "wildcard":
		suffix := make([]byte, 4)
		rand.Read(suffix)
		name := "forgeyard-check-" + hex.EncodeToString(suffix) + "." + c.Domain
		ips, err := s.lookupHost(ctx, name)
		switch {
		case err != nil:
			writeJSON(w, http.StatusOK, domainCheckResponse{Name: name,
				Message: "aucune réponse DNS : l'enregistrement *." + c.Domain + " n'existe pas encore ou ne s'est pas propagé"})
		case !slices.Contains(ips, c.PublicIP):
			writeJSON(w, http.StatusOK, domainCheckResponse{Name: name, Resolved: ips,
				Message: "le wildcard répond, mais pas avec l'IP " + c.PublicIP})
		default:
			writeJSON(w, http.StatusOK, domainCheckResponse{OK: true, Name: name, Resolved: ips,
				Message: "le wildcard pointe bien vers " + c.PublicIP})
		}
	case "provider":
		kind, _ := dns.Lookup(c.Provider)
		provider, err := s.newDNSProvider(c.Provider, c.Creds)
		if err != nil {
			writeJSON(w, http.StatusOK, domainCheckResponse{Message: err.Error()})
			return
		}
		zone, err := dns.FindZone(ctx, provider, c.Domain)
		if err != nil {
			writeJSON(w, http.StatusOK, domainCheckResponse{Message: kind.Label + " : " + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, domainCheckResponse{OK: true,
			Message: kind.Label + " : identifiants valides, zone " + strings.TrimSuffix(zone, ".") + " accessible"})
	default:
		writeError(w, http.StatusBadRequest, "aucun domaine configuré")
	}
}
