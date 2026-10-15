package api

import (
	"slices"
	"testing"
)

func TestKnownImageVolumes(t *testing.T) {
	cases := map[string][]string{
		"grafana/grafana:11.3.0":                {"/var/lib/grafana"},
		"docker.n8n.io/n8nio/n8n":               {"/home/node/.n8n"},
		"ghost:5-alpine":                        {"/var/lib/ghost/content"},
		"lscr.io/linuxserver/sonarr:latest":     {"/config"},
		"ghcr.io/home-assistant/home-assistant": {"/config"},
		"nginx:alpine":                          nil,
		"ghcr.io/moi/mon-app:main":              nil,
	}
	for image, want := range cases {
		if got := knownImageVolumes(image); !slices.Equal(got, want) {
			t.Errorf("%s: %v", image, got)
		}
	}
	if hubPage("postgres:17") != "https://hub.docker.com/_/postgres" || hubPage("grafana/grafana") != "https://hub.docker.com/r/grafana/grafana" || hubPage("ghcr.io/a/b") != "" {
		t.Fatal("hub pages")
	}
}
