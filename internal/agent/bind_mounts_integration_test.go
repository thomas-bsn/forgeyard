//go:build integration

package agent

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/docker"
)

// These integration tests observe files through Docker: config construction is setup, not the oracle.
func TestBindMountsDockerAccessAndDeletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dc, err := docker.New(docker.DefaultHost())
	if err != nil {
		t.Fatal(err)
	}
	networks, err := dc.ListNetworks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	exists := false
	for _, n := range networks {
		if n.Name == networkName {
			exists = true
		}
	}
	if !exists {
		if err = dc.EnsureNetwork(ctx, networkName); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = dc.RemoveNetwork(context.Background(), networkName) })
	}
	source := t.TempDir()
	if base := os.Getenv("FORGEYARD_TEST_BIND_DIR"); base != "" {
		source, err = os.MkdirTemp(base, "forgeyard-bind-test-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(source) })
	}
	if err = os.WriteFile(filepath.Join(source, "note"), []byte("kept"), 0600); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{dc.SocketPath(), filepath.Dir(dc.SocketPath())} {
		if _, err := validateHostMounts(ctx, dc, []*agentpb.BindMount{{Source: source, Target: "/control", ReadOnly: true}}); err == nil || !strings.Contains(err.Error(), "protégées") {
			t.Fatalf("configured Docker socket protection %s: %v", source, err)
		}
	}
	id := time.Now().UnixNano()%1000000000 + 1000000000
	rec := NewReconciler(dc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for i, ro := range []bool{true, false} {
		app := &agentpb.AppSpec{Id: id + int64(i), Name: "bind-check", Image: "alpine:3.21", Running: true, BindAuthorized: true, BindMounts: []*agentpb.BindMount{{Source: canonical, Target: "/files", ReadOnly: ro}}}
		name := containerName(app.Id)
		t.Cleanup(func() { _ = dc.Remove(context.Background(), name) })
		// An idle sandbox-like command keeps the app up without depending on an image's default process.
		config := appContainerConfig(app, appHash(app, "proxy"), "proxy")
		config["Cmd"] = []string{"sleep", "300"}
		if err := rec.checkAppBinds(ctx, app); err != nil {
			t.Fatal(err)
		}
		if err := dc.Create(ctx, name, config); err != nil {
			t.Fatal(err)
		}
		if err := dc.Start(ctx, name); err != nil {
			t.Fatal(err)
		}
		code, out, err := dc.ExecRun(ctx, name, []string{"sh", "-c", "cat /files/note"})
		if err != nil || code != 0 || strings.TrimSpace(out) != "kept" {
			t.Fatalf("read: %d %q %v", code, out, err)
		}
		code, _, err = dc.ExecRun(ctx, name, []string{"sh", "-c", "echo changed > /files/written"})
		if err != nil {
			t.Fatal(err)
		}
		if ro && code == 0 {
			t.Fatal("read-only mount allowed writing")
		}
		if !ro && code != 0 {
			t.Fatal("read/write mount refused writing")
		}
		// The normal deletion only removes named volumes and containers, never the bind source.
		if err := dropAppData(ctx, dc, app.Id); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(source, "note"))
		if err != nil || string(data) != "kept" {
			t.Fatalf("source deleted: %q %v", data, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(source, "written")); err != nil || strings.TrimSpace(string(data)) != "changed" {
		t.Fatalf("host write: %q %v", data, err)
	}
	missing := filepath.Join(source, "missing")
	app := &agentpb.AppSpec{Id: id + 2, Image: "alpine:3.21", Running: true, BindAuthorized: true, BindMounts: []*agentpb.BindMount{{Source: missing, Target: "/files", ReadOnly: true}}}
	if err := rec.createContainer(ctx, containerName(app.Id), app, "missing", "proxy"); err == nil {
		_ = dc.Remove(ctx, containerName(app.Id))
		t.Fatal("missing source accepted")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("source created automatically: %v", err)
	}
	t.Log("bind mount source preserved after removing containers", strconv.FormatInt(id, 10))
}

func TestBindMountsHostNamespaceAndProtectedPaths(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	dc, err := docker.New(docker.DefaultHost())
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, dir := range []string{"srv/media", "srv/private-state", "run", "elsewhere"} {
		if err = os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.Symlink("/srv/media", filepath.Join(root, "absolute")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink("../media", filepath.Join(root, "srv/private-state/relative")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink("/run", filepath.Join(root, "socket-alias")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FORGEYARD_HOST_ROOT", root)
	mounts := func(source string) []*agentpb.BindMount {
		return []*agentpb.BindMount{{Source: source, Target: "/files", ReadOnly: true}}
	}
	for _, source := range []string{"/absolute", "/srv/private-state/relative"} {
		checked, err := validateHostMounts(ctx, dc, mounts(source))
		if err != nil || checked[0].Source != "/srv/media" {
			t.Fatalf("host namespace resolution %s: %v %v", source, checked, err)
		}
	}
	for _, source := range []string{"/", "/socket-alias", "/run", "/missing", "relative"} {
		if _, err := validateHostMounts(ctx, dc, mounts(source)); err == nil {
			t.Fatalf("forbidden source %q accepted", source)
		}
	}
	for _, source := range []string{"/srv", "/srv/private-state"} {
		if _, err := validateHostMounts(ctx, dc, mounts(source), "/srv/private-state"); err == nil {
			t.Fatalf("protected native data %q accepted", source)
		}
	}
	if _, err := validateHostMounts(ctx, dc, mounts("/srv/media"), "/srv/private-state"); err != nil {
		t.Fatalf("unrelated sibling blocked: %v", err)
	}
	// Changing a formerly approved symlink cannot silently change the source at deployment.
	if err = os.Remove(filepath.Join(root, "absolute")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink("/elsewhere", filepath.Join(root, "absolute")); err != nil {
		t.Fatal(err)
	}
	rec := NewReconciler(dc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := rec.checkAppBinds(ctx, &agentpb.AppSpec{BindAuthorized: true, BindMounts: mounts("/absolute")}); err == nil {
		t.Fatal("changed approved path accepted")
	}
}
