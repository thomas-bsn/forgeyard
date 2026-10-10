package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func TestImagePorts(t *testing.T) {
	var calls atomic.Int32
	reg := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/v2/me/app/manifests/latest":
			w.Write([]byte(`{"config":{"digest":"sha256:cfg"}}`))
		case "/v2/me/app/blobs/sha256:cfg":
			w.Write([]byte(`{"config":{"ExposedPorts":{"9120/tcp":{}}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer reg.Close()
	ts, server := newTestServerWithHandle(t)
	server.registryClient = reg.Client()
	admin := newClient()
	post(t, admin, ts.URL+"/api/setup", setupRequest{Token: testToken, InstanceName: "F", Username: "boss",
		Password: "a-long-enough-password", PublicURL: "https://forgeyard.example.com"})
	host := strings.TrimPrefix(reg.URL, "https://")

	ask := func(image string) imagePortsResponse {
		var out imagePortsResponse
		if code := get(t, admin, ts.URL+"/api/images/ports?image="+url.QueryEscape(image), &out); code != http.StatusOK {
			t.Fatalf("%s: %d", image, code)
		}
		return out
	}
	if got := ask(host + "/me/app"); !slices.Equal(got.Ports, []int{9120}) || got.Error != "" {
		t.Fatalf("ports: %+v", got)
	}
	before := calls.Load()
	ask(host + "/me/app")
	if calls.Load() != before {
		t.Fatal("the answer was not remembered")
	}
	if got := ask(host + "/me/missing"); len(got.Ports) != 0 || got.Error == "" {
		t.Fatalf("missing image: %+v", got)
	}
	if code := get(t, newClient(), ts.URL+"/api/images/ports?image=nginx", nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", code)
	}
}
