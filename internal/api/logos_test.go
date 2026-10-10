package api

import (
	"bytes"
	"image"
	"image/png"
	"math/rand"
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
