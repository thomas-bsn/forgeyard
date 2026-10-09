package api

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// DefaultTrustedProxies are the networks a reverse proxy in front of Forgeyard usually sits on: loopback,
// private LANs and Docker networks.
var DefaultTrustedProxies = []netip.Prefix{
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("fc00::/7"),
}

// ParseTrustedProxies reads a comma-separated list of IPs and CIDRs; "none" trusts no proxy.
func ParseTrustedProxies(s string) ([]netip.Prefix, error) {
	if strings.TrimSpace(s) == "none" {
		return nil, nil
	}
	var out []netip.Prefix
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if p, err := netip.ParsePrefix(part); err == nil {
			out = append(out, p.Masked())
			continue
		}
		addr, err := netip.ParseAddr(part)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy %q", part)
		}
		out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return out, nil
}

func (s *Server) trusted(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, p := range s.trustedProxies {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// realClient makes requests that went through a trusted reverse proxy (Caddy, Traefik…) look like they
// came from the real client: RemoteAddr becomes the client address from X-Forwarded-For, and
// X-Forwarded-Proto is kept. From anyone else, the X-Forwarded-* headers are dropped so they cannot be
// forged to dodge the login rate limit or fake HTTPS.
func (s *Server) realClient(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, err := netip.ParseAddrPort(r.RemoteAddr)
		if err != nil || !s.trusted(peer.Addr()) {
			r.Header.Del("X-Forwarded-For")
			r.Header.Del("X-Forwarded-Proto")
			r.Header.Del("X-Forwarded-Host")
			next.ServeHTTP(w, r)
			return
		}
		// The client is the rightmost address not belonging to a trusted proxy.
		hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
		for i := len(hops) - 1; i >= 0; i-- {
			addr, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
			if err != nil {
				break
			}
			if !s.trusted(addr) || i == 0 {
				r.RemoteAddr = net.JoinHostPort(addr.Unmap().String(), "0")
				break
			}
		}
		if p := r.Header.Get("X-Forwarded-Proto"); p != "http" && p != "https" {
			r.Header.Del("X-Forwarded-Proto")
		}
		next.ServeHTTP(w, r)
	})
}
