package api

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestRealClient(t *testing.T) {
	s := &Server{trustedProxies: []netip.Prefix{netip.MustParsePrefix("172.16.0.0/12")}}
	var got *http.Request
	h := s.realClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r }))

	cases := []struct {
		name, remote, xff, proto string
		wantIP, wantProto        string
	}{
		{"through the proxy", "172.18.0.5:4000", "203.0.113.7", "https", "203.0.113.7", "https"},
		{"client-forged hop before the proxy", "172.18.0.5:4000", "1.1.1.1, 203.0.113.7", "https", "203.0.113.7", "https"},
		{"chained trusted proxies", "172.18.0.5:4000", "203.0.113.7, 172.18.0.9", "https", "203.0.113.7", "https"},
		{"direct client forging headers", "198.51.100.4:5000", "10.0.0.1", "https", "198.51.100.4", ""},
		{"proxy without forwarded-for", "172.18.0.5:4000", "", "", "172.18.0.5", ""},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if c.proto != "" {
			r.Header.Set("X-Forwarded-Proto", c.proto)
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
		if ip := clientIP(got); ip != c.wantIP {
			t.Errorf("%s: client IP %s, want %s", c.name, ip, c.wantIP)
		}
		if p := got.Header.Get("X-Forwarded-Proto"); p != c.wantProto {
			t.Errorf("%s: proto %q, want %q", c.name, p, c.wantProto)
		}
	}
}

func TestParseTrustedProxies(t *testing.T) {
	got, err := ParseTrustedProxies("10.0.0.0/8, 192.168.1.10")
	if err != nil || len(got) != 2 || got[1].Bits() != 32 {
		t.Fatalf("%v %v", got, err)
	}
	if got, err := ParseTrustedProxies("none"); err != nil || got != nil {
		t.Fatalf("none: %v %v", got, err)
	}
	if _, err := ParseTrustedProxies("nope"); err == nil {
		t.Fatal("accepted garbage")
	}
}

// Behind Cloudflare's proxy then Caddy, the client is found only when Cloudflare's edge is trusted.
func TestRealClientBehindCloudflare(t *testing.T) {
	private, _ := ParseTrustedProxies("private")
	withCloudflare, _ := ParseTrustedProxies("private, cloudflare")
	for _, c := range []struct {
		trusted []netip.Prefix
		want    string
	}{{private, "162.158.1.2"}, {withCloudflare, "203.0.113.7"}} {
		s := &Server{trustedProxies: c.trusted}
		var got *http.Request
		h := s.realClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r }))
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = "172.18.0.5:4000" // Caddy in Docker
		r.Header.Set("X-Forwarded-For", "203.0.113.7, 162.158.1.2")
		h.ServeHTTP(httptest.NewRecorder(), r)
		if ip := clientIP(got); ip != c.want {
			t.Errorf("trusting %d ranges: client IP %s, want %s", len(c.trusted), ip, c.want)
		}
	}
}
