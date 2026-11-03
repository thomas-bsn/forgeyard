package agent

import (
	"errors"
	"io"
	"log/slog"
	"testing"
)

func TestParseLocalIP(t *testing.T) {
	for in, want := range map[string]string{
		"192.168.1.17":         "192.168.1.17",
		" 10.0.0.4\n":          "10.0.0.4",
		"::ffff:192.168.1.17":  "192.168.1.17",
		"100.64.1.2":           "100.64.1.2", // Tailscale
		"2001:db8::1":          "2001:db8::1",
		"fd7a:115c:a1e0::1234": "fd7a:115c:a1e0::1234",
	} {
		got, err := ParseLocalIP(in)
		if err != nil || got != want {
			t.Errorf("ParseLocalIP(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "localhost", "192.168.1", "192.168.1.17:8090", "300.1.1.1", "127.0.0.1", "::1", "0.0.0.0", "::", "169.254.1.1", "fe80::1", "fe80::1%eth0", "224.0.0.1"} {
		if got, err := ParseLocalIP(bad); err == nil {
			t.Errorf("ParseLocalIP(%q) = %q, want an error", bad, got)
		}
	}
}

func TestLocalIPOverride(t *testing.T) {
	t.Setenv(LocalIPEnv, "")
	if ip, err := LocalIPOverride(); ip != "" || err != nil {
		t.Fatalf("empty: %q %v", ip, err)
	}
	t.Setenv(LocalIPEnv, "192.168.1.17")
	if ip, err := LocalIPOverride(); ip != "192.168.1.17" || err != nil {
		t.Fatalf("set: %q %v", ip, err)
	}
	t.Setenv(LocalIPEnv, "192.168.65.3.1")
	if _, err := LocalIPOverride(); err == nil {
		t.Fatal("invalid address accepted")
	}
}

func TestResolveLocalIP(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Each source fails the test when it is asked while it should not be.
	never := func(t *testing.T, what string) (func() string, func() (string, error)) {
		return func() string { t.Errorf("%s: route asked", what); return "" },
			func() (string, error) { t.Errorf("%s: helper run", what); return "", nil }
	}
	osIs := func(name string) func() (string, error) { return func() (string, error) { return name, nil } }
	helper := func(ip string) func() (string, error) { return func() (string, error) { return ip, nil } }

	t.Run("override wins over Docker Desktop", func(t *testing.T) {
		route, help := never(t, "override")
		got := resolveLocalIP(localIPSources{override: "192.168.1.17", inContainer: true, routeIP: route, dockerOS: osIs(dockerDesktopOS), helperIP: help}, logger)
		if got != "192.168.1.17" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("invalid override detects nothing", func(t *testing.T) {
		route, help := never(t, "invalid")
		got := resolveLocalIP(localIPSources{overrideErr: errors.New("bad"), inContainer: true, routeIP: route, dockerOS: osIs("Ubuntu 24.04 LTS"), helperIP: help}, logger)
		if got != "" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("outside a container: the default route", func(t *testing.T) {
		_, help := never(t, "host")
		got := resolveLocalIP(localIPSources{routeIP: func() string { return "192.168.1.20" }, helperIP: help}, logger)
		if got != "192.168.1.20" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("Docker Desktop: not its VM's address", func(t *testing.T) {
		route, help := never(t, "desktop")
		got := resolveLocalIP(localIPSources{inContainer: true, routeIP: route, dockerOS: osIs(dockerDesktopOS), helperIP: help}, logger)
		if got != "" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("Linux Docker: the helper on the host's network", func(t *testing.T) {
		route, _ := never(t, "linux")
		got := resolveLocalIP(localIPSources{inContainer: true, routeIP: route, dockerOS: osIs("Debian GNU/Linux 12 (bookworm)"), helperIP: helper("192.168.1.30")}, logger)
		if got != "192.168.1.30" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("Docker info failing still runs the helper", func(t *testing.T) {
		route, _ := never(t, "info")
		got := resolveLocalIP(localIPSources{inContainer: true, routeIP: route, dockerOS: func() (string, error) { return "", errors.New("down") }, helperIP: helper("192.168.1.31")}, logger)
		if got != "192.168.1.31" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("helper failing", func(t *testing.T) {
		route, _ := never(t, "fail")
		got := resolveLocalIP(localIPSources{inContainer: true, routeIP: route, dockerOS: osIs("Alpine Linux"), helperIP: func() (string, error) { return "", errors.New("no image") }}, logger)
		if got != "" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("in a container without Docker", func(t *testing.T) {
		route, _ := never(t, "nodocker")
		if got := resolveLocalIP(localIPSources{inContainer: true, routeIP: route}, logger); got != "" {
			t.Fatalf("got %q", got)
		}
	})
}
