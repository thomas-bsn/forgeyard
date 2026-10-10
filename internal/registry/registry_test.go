package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestParseRef(t *testing.T) {
	for image, want := range map[string]Ref{
		"nginx":                         {"registry-1.docker.io", "library/nginx", "latest"},
		"nginx:1.27":                    {"registry-1.docker.io", "library/nginx", "1.27"},
		"docker.io/traefik/whoami":      {"registry-1.docker.io", "traefik/whoami", "latest"},
		"ghcr.io/Moghtech/komodo:2":     {"ghcr.io", "moghtech/komodo", "2"},
		"localhost:5000/app":            {"localhost:5000", "app", "latest"},
		"quay.io/a/b@sha256:abc":        {"quay.io", "a/b", "sha256:abc"},
		"registry.example.com:8443/x/y": {"registry.example.com:8443", "x/y", "latest"},
	} {
		got, err := ParseRef(image)
		if err != nil || got != want {
			t.Errorf("ParseRef(%q) = %+v, %v; want %+v", image, got, err, want)
		}
	}
	if _, err := ParseRef("bad name"); err == nil {
		t.Error("an image name with a space was accepted")
	}
}

// A registry that wants a token, serves a multi-platform index, and an image exposing 3000/tcp,
// 53/udp and 9000.
func TestExposedPorts(t *testing.T) {
	var ts *httptest.Server
	ts = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			if r.URL.Query().Get("scope") != "repository:team/app:pull" {
				http.Error(w, "bad scope", http.StatusBadRequest)
				return
			}
			w.Write([]byte(`{"token":"t0k"}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer t0k" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+ts.URL+`/token",service="test",scope="repository:team/app:pull"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v2/team/app/manifests/1.0":
			w.Write([]byte(`{"manifests":[{"digest":"sha256:arm","platform":{"os":"linux","architecture":"arm64"}},
				{"digest":"sha256:amd","platform":{"os":"linux","architecture":"amd64"}}]}`))
		case "/v2/team/app/manifests/sha256:amd":
			w.Write([]byte(`{"config":{"digest":"sha256:cfg"}}`))
		case "/v2/team/app/blobs/sha256:cfg":
			w.Write([]byte(`{"config":{"ExposedPorts":{"3000/tcp":{},"53/udp":{},"9000":{}},"Volumes":{"/data":{},"/config":{}}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	host := strings.TrimPrefix(ts.URL, "https://")

	ports, err := ExposedPorts(context.Background(), ts.Client(), host+"/team/app:1.0")
	if err != nil || !slices.Equal(ports, []int{3000, 9000}) {
		t.Fatalf("ports: %v, %v", ports, err)
	}
	cfg, err := ImageConfig(context.Background(), ts.Client(), host+"/team/app:1.0")
	if err != nil || !slices.Equal(cfg.Volumes, []string{"/config", "/data"}) {
		t.Fatalf("volumes: %v, %v", cfg.Volumes, err)
	}
	if _, err := ExposedPorts(context.Background(), ts.Client(), host+"/team/app:missing"); err == nil {
		t.Fatal("a missing tag gave ports")
	}
}

func TestPublicClientRefusesLocalAddresses(t *testing.T) {
	ts := httptest.NewTLSServer(http.NotFoundHandler())
	defer ts.Close()
	_, err := ExposedPorts(context.Background(), PublicClient(), strings.TrimPrefix(ts.URL, "https://")+"/x")
	if err == nil || !strings.Contains(err.Error(), "non publique") {
		t.Fatalf("loopback registry: %v", err)
	}
}
