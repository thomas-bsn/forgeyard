package agent

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/docker"
)

// Needs a Docker daemon: FORGEYARD_DOCKER_TEST=1 go test ./internal/agent/
func TestReplaceAgainstDocker(t *testing.T) {
	if os.Getenv("FORGEYARD_DOCKER_TEST") == "" {
		t.Skip("set FORGEYARD_DOCKER_TEST=1 to run against a real Docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dc, err := docker.New(docker.DefaultHost())
	if err != nil {
		t.Fatal(err)
	}
	const name = "forgeyard-replace-test"
	cleanup := func() {
		dc.Remove(context.Background(), name)
		dc.Remove(context.Background(), name+"-previous")
		dc.RemoveVolume(context.Background(), name)
	}
	cleanup()
	defer cleanup()
	for _, img := range []string{"alpine:3.21", "alpine:3.20"} {
		if err := dc.Pull(ctx, img); err != nil {
			t.Fatal(err)
		}
	}
	if err := dc.Create(ctx, name, map[string]any{
		"Image": "alpine:3.21", "Cmd": []string{"sleep", "300"}, "Env": []string{"KEEP=me"},
		"HostConfig": map[string]any{"Binds": []string{name + ":/state"}, "RestartPolicy": map[string]any{"Name": "unless-stopped"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := dc.Start(ctx, name); err != nil {
		t.Fatal(err)
	}
	before, _ := dc.InspectRaw(ctx, name)

	if err := Replace(ctx, dc, name, "alpine:3.20"); err != nil {
		t.Fatal(err)
	}
	after, err := dc.InspectRaw(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	cfg := after["Config"].(map[string]any)
	host := after["HostConfig"].(map[string]any)
	state := after["State"].(map[string]any)
	if cfg["Image"] != "alpine:3.20" || state["Running"] != true || after["Id"] == before["Id"] {
		t.Fatalf("replaced container: image %v, running %v", cfg["Image"], state["Running"])
	}
	if !slices.Contains(toStrings(cfg["Env"]), "KEEP=me") || !slices.Equal(toStrings(host["Binds"]), []string{name + ":/state"}) ||
		host["RestartPolicy"].(map[string]any)["Name"] != "unless-stopped" || cfg["Hostname"] == before["Config"].(map[string]any)["Hostname"] {
		t.Fatalf("settings not kept: %v %v", cfg, host)
	}
	if _, err := dc.Inspect(ctx, name+"-previous"); err == nil {
		t.Fatal("previous container left behind")
	}

	// An image that cannot start brings the previous version back.
	if err := Replace(ctx, dc, name, "forgeyard-no-such-image:never"); err == nil {
		t.Fatal("replaced with a missing image")
	}
	ct, err := dc.Inspect(ctx, name)
	if err != nil || !ct.State.Running || ct.Config.Image != "alpine:3.20" {
		t.Fatalf("previous version not restored: %+v, %v", ct.Config, err)
	}
}

func toStrings(v any) []string {
	var out []string
	l, _ := v.([]any)
	for _, x := range l {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}
