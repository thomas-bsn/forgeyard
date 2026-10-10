package agent

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/docker"
)

func TestVolumeName(t *testing.T) {
	a, b := volumeName(7, "/var/lib/postgresql/data"), volumeName(7, "/var/lib/postgresql-data")
	if !strings.HasPrefix(a, "forgeyard-app-7-var-lib-postgresql-data-") || a == b {
		t.Fatalf("names: %s %s", a, b)
	}
	if volumeName(7, "/data") != volumeName(7, "/data") {
		t.Fatal("not stable")
	}
}

// Needs a Docker daemon: FORGEYARD_DOCKER_TEST=1 go test ./internal/agent/
func TestVolumesAgainstDocker(t *testing.T) {
	if os.Getenv("FORGEYARD_DOCKER_TEST") == "" {
		t.Skip("set FORGEYARD_DOCKER_TEST=1 to run against a real Docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dc, err := docker.New(docker.DefaultHost())
	if err != nil {
		t.Fatal(err)
	}
	const appID = 990001
	app := &agentpb.AppSpec{Id: appID, Name: "voltest", Image: "alpine:3.21", Volumes: []string{"/data"}}
	defer dropAppData(context.Background(), dc, appID)
	if err := dc.Pull(ctx, "alpine:3.21"); err != nil {
		t.Fatal(err)
	}
	run := func(cmd string) string {
		t.Helper()
		name := containerName(appID)
		dc.Remove(ctx, name)
		if err := dc.Create(ctx, name, map[string]any{
			"Image": "alpine:3.21", "Cmd": []string{"sh", "-c", cmd}, "Labels": map[string]string{labelApp: "990001"},
			"HostConfig": map[string]any{"Mounts": volumeMounts(app)},
		}); err != nil {
			t.Fatal(err)
		}
		if err := dc.Start(ctx, name); err != nil {
			t.Fatal(err)
		}
		dc.Wait(ctx, name)
		out, _ := dc.Output(ctx, name)
		return strings.TrimSpace(out)
	}
	run("echo kept > /data/file")
	if got := run("cat /data/file"); got != "kept" {
		t.Fatalf("a new container of the app sees %q", got)
	}
	vols, err := dc.ListVolumes(ctx, labelApp+"=990001")
	if err != nil || len(vols) != 1 || vols[0].Labels[labelVolumePath] != "/data" {
		t.Fatalf("volumes: %+v %v", vols, err)
	}
	if err := dropAppData(ctx, dc, appID); err != nil {
		t.Fatal(err)
	}
	if vols, _ := dc.ListVolumes(ctx, labelApp+"=990001"); len(vols) != 0 {
		t.Fatalf("left after the app: %+v", vols)
	}
}
