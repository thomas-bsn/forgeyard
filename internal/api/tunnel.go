package api

import (
	"context"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

// A tunneled node receives its visits without any inbound port: Forgeyard's machine's Traefik relays its
// apps' domains to the server, which passes each request on to the node's Traefik through the node's
// agent (nodes.Hub.DialTunnel).

// tunnelTarget is where Forgeyard's machine's Traefik sends a tunneled node's apps: the server's HTTP
// port on that machine.
func (s *Server) tunnelTarget(front db.Node) string {
	ip := front.LocalIp
	if ip == "" {
		_, ip = s.localProbe()
	}
	return "http://" + net.JoinHostPort(ip, s.httpPort)
}

// setTunneledHosts replaces the app domains served through a tunnel, with their nodes.
func (s *Server) setTunneledHosts(hosts map[string]int64) {
	s.tunnelMu.Lock()
	s.tunneledHosts = hosts
	s.tunnelMu.Unlock()
}

func (s *Server) tunneledNode(host string) (int64, bool) {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	s.tunnelMu.RLock()
	defer s.tunnelMu.RUnlock()
	id, ok := s.tunneledHosts[strings.ToLower(host)]
	return id, ok
}

// tunnelHost is the address the tunnel proxy dials for a node; its transport keeps idle connections per
// node.
func tunnelHost(nodeID int64) string {
	return "node-" + strconv.FormatInt(nodeID, 10) + ".tunnel:80"
}

// tunnelTransport reaches a node's Traefik through its agent.
func (s *Server) tunnelTransport() *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			name, _, _ := strings.Cut(strings.TrimPrefix(addr, "node-"), ".")
			id, err := strconv.ParseInt(name, 10, 64)
			if err != nil {
				return nil, err
			}
			return s.nodes.DialTunnel(ctx, id)
		},
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     90 * time.Second,
	}
}

// tunnelIngress serves the requests for tunneled apps' domains, and passes the others on.
func (s *Server) tunnelIngress(next http.Handler) http.Handler {
	proxy := &httputil.ReverseProxy{
		Director: func(r *http.Request) {
			id, _ := s.tunneledNode(r.Host)
			r.URL.Scheme, r.URL.Host = "http", tunnelHost(id) // the Host header stays the app's domain
		},
		Transport: s.tunnelTransport(),
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			s.logger.Warn("passing a request through a tunnel failed", "host", r.Host, "err", err)
			http.Error(w, "Cette app est injoignable pour le moment : son node ne répond pas.", http.StatusBadGateway)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.tunneledNode(r.Host); ok {
			proxy.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// checkTunnel tries both legs of a tunneled node's visits: from Forgeyard's machine to the server's HTTP
// port, then through the node's agent to its Traefik.
func (s *Server) checkTunnel(ctx context.Context, node db.Node) (reach, detail string) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if front, err := s.store.GetLocalNode(ctx); err == nil {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.tunnelTarget(front)+"/api/health", nil)
		resp, err := s.probeClient.Do(req)
		if err != nil {
			return "error", "la machine de Forgeyard n'atteint pas son propre port " + s.httpPort + " (" + strings.TrimPrefix(s.tunnelTarget(front), "http://") + ") : il doit être publié sur la machine"
		}
		resp.Body.Close()
	}
	transport := s.tunnelTransport()
	defer transport.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+tunnelHost(node.ID)+"/", nil)
	req.Host = "forgeyard-check.invalid" // no app answers to it: any answer of Traefik will do
	resp, err := transport.RoundTrip(req)
	if err != nil {
		return "error", "le tunnel ne passe pas : regarde les logs de l'agent"
	}
	resp.Body.Close()
	return "ok", ""
}
