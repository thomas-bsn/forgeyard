package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/cloudflare"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

const (
	settingAppsDomainMode   = "apps_domain_mode" // none, wildcard or cloudflare
	settingAppsDomain       = "apps_domain"
	settingAppsPublicIP     = "apps_public_ip"
	settingCloudflareToken  = "cloudflare_api_token" // encrypted
	settingCloudflareZoneID = "cloudflare_zone_id"
	settingCloudflareZone   = "cloudflare_zone_name"
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

type domainSettings struct {
	PublicURL          string `json:"publicUrl"`
	DiscordRedirectURL string `json:"discordRedirectUrl"`
	Mode               string `json:"mode"`
	Domain             string `json:"domain"`
	PublicIP           string `json:"publicIp"`
	CloudflareHasToken bool   `json:"cloudflareHasToken"`
	CloudflareZone     string `json:"cloudflareZone,omitempty"`
}

func (s *Server) loadDomainSettings(ctx context.Context, r *http.Request) (domainSettings, error) {
	var d domainSettings
	var err error
	if d.PublicURL, err = s.publicURL(ctx, r); err != nil {
		return d, err
	}
	if d.DiscordRedirectURL, err = s.discordRedirectURL(ctx, r); err != nil {
		return d, err
	}
	q := s.store.Queries
	for key, dst := range map[string]*string{
		settingAppsDomainMode: &d.Mode, settingAppsDomain: &d.Domain,
		settingAppsPublicIP: &d.PublicIP, settingCloudflareZone: &d.CloudflareZone,
	} {
		if *dst, err = s.setting(ctx, q, key); err != nil {
			return d, err
		}
	}
	if d.Mode == "" {
		d.Mode = "none"
	}
	token, err := s.setting(ctx, q, settingCloudflareToken)
	d.CloudflareHasToken = token != ""
	return d, err
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
	PublicURL       string `json:"publicUrl"`
	Mode            string `json:"mode"`
	Domain          string `json:"domain"`
	PublicIP        string `json:"publicIp"`
	CloudflareToken string `json:"cloudflareToken"` // empty keeps the stored token
}

// handlePutDomainSettings saves Forgeyard's address and how app domains are handled. In Cloudflare mode the
// token and the zone are checked with Cloudflare before anything is saved.
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
	case "wildcard", "cloudflare":
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

	var sealedToken string
	var zone cloudflare.Zone
	if body.Mode == "cloudflare" {
		token := strings.TrimSpace(body.CloudflareToken)
		if token == "" {
			var err error
			if token, err = s.cloudflareToken(ctx); err != nil {
				s.internalError(w, r, err)
				return
			}
			if token == "" {
				writeError(w, http.StatusBadRequest, "le token API Cloudflare est obligatoire")
				return
			}
		}
		cf := s.newCloudflare(token)
		if err := cf.VerifyToken(ctx); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		var err error
		if zone, err = cf.FindZone(ctx, domain); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if sealedToken, err = s.secrets.Encrypt(token, settingCloudflareToken); err != nil {
			s.internalError(w, r, err)
			return
		}
	}

	err := s.store.InTx(ctx, func(q *db.Queries) error {
		values := map[string]string{
			settingPublicURL: publicURL, settingAppsDomainMode: body.Mode,
			settingAppsDomain: domain, settingAppsPublicIP: ip,
		}
		if body.Mode == "cloudflare" {
			values[settingCloudflareToken] = sealedToken
			values[settingCloudflareZoneID] = zone.ID
			values[settingCloudflareZone] = zone.Name
		}
		for k, v := range values {
			if err := q.SetSetting(ctx, db.SetSettingParams{Key: k, Value: v}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.logger.Info("domain settings changed", "public_url", publicURL, "mode", body.Mode, "domain", domain, "by", currentUser(r).DisplayName)
	s.handleGetDomainSettings(w, r)
}

func (s *Server) cloudflareToken(ctx context.Context) (string, error) {
	sealed, err := s.setting(ctx, s.store.Queries, settingCloudflareToken)
	if err != nil || sealed == "" {
		return "", err
	}
	return s.secrets.Decrypt(sealed, settingCloudflareToken)
}

type domainCheckResponse struct {
	OK       bool     `json:"ok"`
	Message  string   `json:"message"`
	Name     string   `json:"name,omitempty"`
	Resolved []string `json:"resolved,omitempty"`
}

// handleCheckDomain tests the saved configuration: in wildcard mode, that a random subdomain resolves to the
// public IP; in Cloudflare mode, that the token still reaches the zone.
func (s *Server) handleCheckDomain(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	d, err := s.loadDomainSettings(ctx, r)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	switch d.Mode {
	case "wildcard":
		suffix := make([]byte, 4)
		rand.Read(suffix)
		name := "forgeyard-check-" + hex.EncodeToString(suffix) + "." + d.Domain
		ips, err := s.lookupHost(ctx, name)
		switch {
		case err != nil:
			writeJSON(w, http.StatusOK, domainCheckResponse{Name: name,
				Message: "aucune réponse DNS : l'enregistrement *." + d.Domain + " n'existe pas encore ou ne s'est pas propagé"})
		case !slices.Contains(ips, d.PublicIP):
			writeJSON(w, http.StatusOK, domainCheckResponse{Name: name, Resolved: ips,
				Message: "le wildcard répond, mais pas avec l'IP " + d.PublicIP})
		default:
			writeJSON(w, http.StatusOK, domainCheckResponse{OK: true, Name: name, Resolved: ips,
				Message: "le wildcard pointe bien vers " + d.PublicIP})
		}
	case "cloudflare":
		token, err := s.cloudflareToken(ctx)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		cf := s.newCloudflare(token)
		if err := cf.VerifyToken(ctx); err != nil {
			writeJSON(w, http.StatusOK, domainCheckResponse{Message: err.Error()})
			return
		}
		zone, err := cf.FindZone(ctx, d.Domain)
		if err != nil {
			writeJSON(w, http.StatusOK, domainCheckResponse{Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, domainCheckResponse{OK: true, Message: "token valide, zone " + zone.Name + " accessible"})
	default:
		writeError(w, http.StatusBadRequest, "aucun domaine configuré")
	}
}
