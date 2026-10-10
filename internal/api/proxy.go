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

// CloudflareProxies are Cloudflare's published edge ranges (https://www.cloudflare.com/ips/), for an
// address served through Cloudflare's proxy.
var CloudflareProxies = mustPrefixes(
	"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22", "141.101.64.0/18",
	"108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20", "197.234.240.0/22", "198.41.128.0/17",
	"162.158.0.0/15", "104.16.0.0/13", "104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
	"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32", "2405:8100::/32",
	"2a06:98c0::/29", "2c0f:f248::/32",
)

func mustPrefixes(list ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(list))
	for i, s := range list {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}

// ParseTrustedProxies reads a comma-separated list of IPs, CIDRs and the keywords "private" (the default
// networks) and "cloudflare" (Cloudflare's edge); "none" trusts no proxy.
func ParseTrustedProxies(s string) ([]netip.Prefix, error) {
	if strings.TrimSpace(s) == "none" {
		return nil, nil
	}
	var out []netip.Prefix
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		switch part {
		case "":
			continue
		case "private":
			out = append(out, DefaultTrustedProxies...)
			continue
		case "cloudflare":
			out = append(out, CloudflareProxies...)
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
