package api

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"math/rand"
	"net/http"
	"testing"
)

func TestHubRepo(t *testing.T) {
	for image, want := range map[string]string{
		"nginx":                          "library/nginx",
		"nginx:1.27-alpine":              "library/nginx",
		"grafana/grafana:latest":         "grafana/grafana",
		"docker.io/louislam/uptime-kuma": "louislam/uptime-kuma",
		"nginx@sha256:abc":               "library/nginx",
		"ghcr.io/thomas-bsn/forgeyard":   "",
		"localhost:5000/app":             "",
		"registry.example.com:5000/a/b":  "",
	} {
		if got := hubRepo(image); got != want {
			t.Errorf("hubRepo(%q) = %q, want %q", image, got, want)
		}
	}
}

func TestShrinkLogo(t *testing.T) {
	var buf bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, 900, 600))
	rand.Read(img.Pix) // noise, so the PNG stays large
	png.Encode(&buf, img)
	data, ct := shrinkLogo(buf.Bytes(), "image/png")
	if ct != "image/png" {
		t.Fatalf("type %q", ct)
	}
	out, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if b := out.Bounds(); b.Dx() != logoSize || b.Dy() != 85 {
		t.Fatalf("size %v", b)
	}
}

func TestInstanceIcon(t *testing.T) {
	ts, _ := newTestServerWithHandle(t)
	var buf bytes.Buffer
	png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 8, 8)))
	icon := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	admin := newClient()
	if resp := post(t, admin, ts.URL+"/api/setup", setupRequest{Token: testToken, InstanceName: "F", Username: "boss",
		Password: "a-long-enough-password", PublicURL: "https://forgeyard.example.com", Icon: icon}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("setup with icon: %d", resp.StatusCode)
	}
	var inst instanceResponse
	get(t, newClient(), ts.URL+"/api/instance", &inst)
	if inst.IconURL == "" {
		t.Fatal("no icon URL")
	}
	// Public: the sign-in page shows it before anyone signs in.
	resp, err := http.Get(ts.URL + inst.IconURL)
	if err != nil || resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("icon: %v %v", resp, err)
	}
	if code := del(t, admin, ts.URL+"/api/admin/settings/icon"); code != http.StatusNoContent {
		t.Fatalf("remove icon: %d", code)
	}
	var after instanceResponse
	get(t, newClient(), ts.URL+"/api/instance", &after)
	if after.IconURL != "" {
		t.Fatal("icon still set")
	}
}
