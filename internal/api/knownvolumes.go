package api

import (
	"strings"

	"github.com/thomas-bsn/forgeyard/internal/registry"
)

// Many images keep their data in a folder without declaring it (no VOLUME in their Dockerfile): the app form
// would offer nothing, and the data would go at the next redeployment. These are the folders their docs give,
// by repository (whatever the registry: lscr.io/linuxserver/x and linuxserver/x are the same image).
var knownVolumes = map[string][]string{
	"grafana/grafana":               {"/var/lib/grafana"},
	"grafana/grafana-oss":           {"/var/lib/grafana"},
	"n8nio/n8n":                     {"/home/node/.n8n"},
	"library/ghost":                 {"/var/lib/ghost/content"},
	"gitea/gitea":                   {"/data"},
	"jellyfin/jellyfin":             {"/config", "/cache"},
	"louislam/uptime-kuma":          {"/app/data"},
	"vaultwarden/server":            {"/data"},
	"nodered/node-red":              {"/data"},
	"portainer/portainer-ce":        {"/data"},
	"homeassistant/home-assistant":  {"/config"},
	"home-assistant/home-assistant": {"/config"},
	"pihole/pihole":                 {"/etc/pihole"},
	"adguard/adguardhome":           {"/opt/adguardhome/work", "/opt/adguardhome/conf"},
	"filebrowser/filebrowser":       {"/srv"},
	"syncthing/syncthing":           {"/var/syncthing"},
	"minio/minio":                   {"/data"},
	"library/influxdb":              {"/var/lib/influxdb2"},
	"dpage/pgadmin4":                {"/var/lib/pgadmin"},
	"codercom/code-server":          {"/home/coder"},
	"ollama/ollama":                 {"/root/.ollama"},
	"open-webui/open-webui":         {"/app/backend/data"},
	"freshrss/freshrss":             {"/var/www/FreshRSS/data"},
	"paperless-ngx/paperless-ngx":   {"/usr/src/paperless/data", "/usr/src/paperless/media"},
	"actualbudget/actual-server":    {"/data"},
	"sissbruecker/linkding":         {"/etc/linkding/data"},
}

// knownImageVolumes are the data folders of a well-known image that does not declare them. The images of
// linuxserver.io all keep theirs in /config.
func knownImageVolumes(image string) []string {
	ref, err := registry.ParseRef(image)
	if err != nil {
		return nil
	}
	if v, ok := knownVolumes[ref.Repo]; ok {
		return v
	}
	if strings.HasPrefix(ref.Repo, "linuxserver/") {
		return []string{"/config"}
	}
	return nil
}

// hubPage is an image's page on Docker Hub, where its docs say which folders to keep; "" for another registry.
func hubPage(image string) string {
	ref, err := registry.ParseRef(image)
	if err != nil || ref.Host != "registry-1.docker.io" {
		return ""
	}
	if name, ok := strings.CutPrefix(ref.Repo, "library/"); ok {
		return "https://hub.docker.com/_/" + name
	}
	return "https://hub.docker.com/r/" + ref.Repo
}
