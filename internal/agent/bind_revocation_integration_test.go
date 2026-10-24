//go:build integration

package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/docker"
)

func TestBindRevocationStopsAndCannotRestartDockerContainer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	dc, err := docker.New(docker.DefaultHost())
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	if base := os.Getenv("FORGEYARD_TEST_BIND_DIR"); base != "" {
		source, err = os.MkdirTemp(base, "forgeyard-revoke-test-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(source) })
	}
	if err = os.WriteFile(filepath.Join(source, "note"), []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	app := &agentpb.AppSpec{Id: time.Now().UnixNano()%1000000000 + 2000000000, Name: "revocation-check", Image: "alpine:3.21", Running: true, BindAuthorized: true, BindMounts: []*agentpb.BindMount{{Source: source, Target: "/files", ReadOnly: false}}}
	name := containerName(app.Id)
	t.Cleanup(func() { _ = dc.Remove(context.Background(), name) })
	config := appContainerConfig(app, appHash(app, "proxy"), "proxy")
	config["Cmd"] = []string{"sleep", "300"}
	config["HostConfig"].(map[string]any)["NetworkMode"] = "none"
	delete(config, "NetworkingConfig")
	if err = dc.Create(ctx, name, config); err != nil {
		t.Fatal(err)
	}
	if err = dc.Start(ctx, name); err != nil {
		t.Fatal(err)
	}
	if ct, err := dc.Inspect(ctx, name); err != nil || !ct.State.Running {
		t.Fatalf("fixture not running: %+v %v", ct.State, err)
	}
	app.BindAuthorized = false
	// Even an inconsistent running request cannot keep the old container alive or start a new one.
	rec := NewReconciler(dc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err = rec.ensureApp(ctx, app, "proxy"); err == nil {
		t.Fatal("revoked access accepted")
	}
	if ct, err := dc.Inspect(ctx, name); err != nil || ct.State.Running {
		t.Fatalf("revocation did not stop container: %+v %v", ct.State, err)
	}
	// A new reconciler represents an agent restart/reconnection with the persisted authorization.
	rec = NewReconciler(dc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err = rec.ensureApp(ctx, app, "proxy"); err == nil {
		t.Fatal("new agent restored revoked access")
	}
	if ct, err := dc.Inspect(ctx, name); err != nil || ct.State.Running {
		t.Fatalf("container restarted: %+v %v", ct.State, err)
	}
	app.Running = false
	if err = rec.ensureApp(ctx, app, "proxy"); err != nil {
		t.Fatal(err)
	}
	if ct, err := dc.Inspect(ctx, name); !errors.Is(err, docker.ErrNotFound) && (err != nil || ct.State.Running) {
		t.Fatalf("stopped desired restored container: %+v %v", ct.State, err)
	}
	if data, err := os.ReadFile(filepath.Join(source, "note")); err != nil || string(data) != "retained" {
		t.Fatalf("host source altered: %q %v", data, err)
	}
}
