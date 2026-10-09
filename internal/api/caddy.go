package api

import (
	"net/http"
	"net/url"
	"strings"
)

// handleCaddyAsk answers Caddy's on-demand TLS check: 200 when the domain belongs to an app (or to Forgeyard
// itself), so Caddy only requests certificates for real apps.
//
//	{ on_demand_tls { ask http://forgeyard:8080/api/caddy/ask } }
func (s *Server) handleCaddyAsk(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	domain := strings.TrimSuffix(strings.ToLower(r.URL.Query().Get("domain")), ".")
	if base, err := s.publicURL(ctx, r); err == nil {
		if u, err := url.Parse(base); err == nil && u.Hostname() == domain {
			w.WriteHeader(http.StatusOK)
			return
		}
	}
	c, err := s.loadDNSConfig(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	name, ok := strings.CutSuffix(domain, "."+c.Domain)
	if !ok || c.Mode == "none" || c.Domain == "" || strings.Contains(name, ".") {
		http.NotFound(w, r)
		return
	}
	exists, err := s.store.AppExistsByName(ctx, name)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if exists == 0 {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusOK)
}
