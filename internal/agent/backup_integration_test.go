//go:build integration

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

// TestBackupRestoreWithMinIO saves a postgres database and a volume's files to a local MinIO with restic,
// then restores both into new volumes and checks the data came back. It needs Docker:
//
//	go test -tags integration -run TestBackupRestoreWithMinIO ./internal/agent/
//
// FORGEYARD_TEST_MINIO_IMAGE picks the MinIO image.
func TestBackupRestoreWithMinIO(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	dc, err := docker.New(docker.DefaultHost())
	if err != nil {
		t.Fatal(err)
	}
	const (
		net      = "fy-test-backup-net"
		minio    = "fy-test-minio"
		pg       = "fy-test-pg"
		pgData   = "fy-test-pgdata"
		pgImage  = "postgres:16-alpine"
		files    = "fy-test-files"
		restored = "fy-test-pgdata-restored"
		filesOut = "fy-test-files-restored"
		pg2      = "fy-test-pg-restored"
	)
	cleanup := func() {
		c := context.Background()
		for _, n := range []string{minio, pg, pg2, "fy-test-writer", "fy-test-reader"} {
			dc.Remove(c, n)
		}
		for _, v := range []string{pgData, files, restored, filesOut} {
			dc.RemoveVolume(c, v)
		}
		dc.RemoveNetwork(c, net)
	}
	cleanup()
	t.Cleanup(cleanup)

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(dc.EnsureNetwork(ctx, net))
	// MinIO no longer publishes images on Docker Hub: Chainguard's build, or another one.
	minioImage := os.Getenv("FORGEYARD_TEST_MINIO_IMAGE")
	if minioImage == "" {
		minioImage = "cgr.dev/chainguard/minio:latest"
	}
	must(dc.Pull(ctx, minioImage))
	must(dc.Pull(ctx, pgImage))
	must(dc.Create(ctx, minio, map[string]any{
		"Image": minioImage, "Entrypoint": []string{"minio"}, "Cmd": []string{"server", "/tmp/minio"},
		"Env":        []string{"MINIO_ROOT_USER=forgeyard", "MINIO_ROOT_PASSWORD=forgeyard-secret"},
		"HostConfig": map[string]any{"NetworkMode": net},
	}))
	must(dc.Start(ctx, minio))

	// A database with a row.
	pgSpec := &agentpb.Postgres{Container: pg, User: "shameless", Database: "shameless", Password: "pw", Image: pgImage, DataPath: "/var/lib/postgresql/data"}
	must(dc.Create(ctx, pg, map[string]any{
		"Image": pgImage, "Env": []string{"POSTGRES_USER=shameless", "POSTGRES_DB=shameless", "POSTGRES_PASSWORD=pw"},
		"HostConfig": map[string]any{"Mounts": []map[string]any{{"Type": "volume", "Source": pgData, "Target": "/var/lib/postgresql/data"}}},
	}))
	must(dc.Start(ctx, pg))
	must(waitPostgres(ctx, dc, pg, pgSpec))
	psql := func(container, sql string) string {
		t.Helper()
		code, out, err := dc.ExecRun(ctx, container, []string{"psql", "-U", "shameless", "-d", "shameless", "-tAc", sql})
		if err != nil || code != 0 {
			t.Fatalf("psql %q: %d %v %s", sql, code, err, out)
		}
		return strings.TrimSpace(out)
	}
	psql(pg, "CREATE TABLE confessions (id serial PRIMARY KEY, text text); INSERT INTO confessions (text) VALUES ('hello'), ('world')")

	// A volume with a file.
	must(dc.CreateVolume(ctx, files, nil))
	res, err := dc.Run(ctx, "fy-test-writer", map[string]any{
		"Image": pgImage, "Cmd": []string{"sh", "-c", "echo kept > /data/note.txt"},
		"HostConfig": map[string]any{"Mounts": []map[string]any{{"Type": "volume", "Source": files, "Target": "/data"}}},
	}, nil)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("writing the file: %v %+v", err, res)
	}

	restic := &agentpb.Restic{Repository: "s3:http://" + minio + ":9000/backups/forgeyard", Password: "repo-password",
		AccessKey: "forgeyard", SecretKey: "forgeyard-secret", Network: net}
	job := func(id, op string, mod func(*agentpb.BackupJob)) *agentpb.BackupResult {
		t.Helper()
		j := &agentpb.BackupJob{Id: id, Op: op, Restic: restic, Keep: 2}
		mod(j)
		return runBackupJob(ctx, dc, j)
	}

	// MinIO takes a moment to start.
	var r *agentpb.BackupResult
	for i := 0; i < 30; i++ {
		r = job("it-db", "backup", func(j *agentpb.BackupJob) { j.Tag, j.Volume, j.Postgres = "policy-1", pgData, pgSpec })
		if r.GetError() == "" {
			break
		}
		time.Sleep(time.Second)
	}
	if r.GetError() != "" || r.GetSnapshot() == "" {
		t.Fatalf("database backup: %s\n%s", r.GetError(), r.GetLog())
	}
	// Three more: only the last two are kept.
	for i := 0; i < 3; i++ {
		if r := job("it-db", "backup", func(j *agentpb.BackupJob) { j.Tag, j.Volume, j.Postgres = "policy-1", pgData, pgSpec }); r.GetError() != "" {
			t.Fatalf("backup %d: %s\n%s", i, r.GetError(), r.GetLog())
		}
	}
	snaps := job("it-ls", "snapshots", func(j *agentpb.BackupJob) { j.Tag = "policy-1" })
	if snaps.GetError() != "" || len(snaps.GetSnapshots()) != 2 {
		t.Fatalf("snapshots after retention: %s %v", snaps.GetError(), snaps.GetSnapshots())
	}
	if r := job("it-files", "backup", func(j *agentpb.BackupJob) { j.Tag, j.Volume = "policy-2", files }); r.GetError() != "" {
		t.Fatalf("files backup: %s\n%s", r.GetError(), r.GetLog())
	}

	// The database comes back in a new volume, which a postgres starts on.
	r = job("it-restore", "restore", func(j *agentpb.BackupJob) {
		j.Tag, j.Postgres, j.Snapshot, j.TargetVolume = "policy-1", pgSpec, snaps.GetSnapshots()[0].GetId(), restored
	})
	if r.GetError() != "" {
		t.Fatalf("database restore: %s\n%s", r.GetError(), r.GetLog())
	}
	must(dc.Create(ctx, pg2, map[string]any{
		"Image": pgImage, "Env": []string{"POSTGRES_PASSWORD=pw"},
		"HostConfig": map[string]any{"Mounts": []map[string]any{{"Type": "volume", "Source": restored, "Target": "/var/lib/postgresql/data"}}},
	}))
	must(dc.Start(ctx, pg2))
	must(waitPostgres(ctx, dc, pg2, pgSpec))
	if got := psql(pg2, "SELECT string_agg(text, ',' ORDER BY id) FROM confessions"); got != "hello,world" {
		t.Fatalf("restored rows: %q", got)
	}
	// A restore never replaces a volume.
	r = job("it-restore", "restore", func(j *agentpb.BackupJob) {
		j.Tag, j.Postgres, j.Snapshot, j.TargetVolume = "policy-1", pgSpec, snaps.GetSnapshots()[0].GetId(), restored
	})
	if !strings.Contains(r.GetError(), "existe déjà") {
		t.Fatalf("restore over an existing volume: %q", r.GetError())
	}

	// The files come back too.
	fsnaps := job("it-ls", "snapshots", func(j *agentpb.BackupJob) { j.Tag = "policy-2" })
	if len(fsnaps.GetSnapshots()) != 1 {
		t.Fatalf("file snapshots: %s %v", fsnaps.GetError(), fsnaps.GetSnapshots())
	}
	r = job("it-restore-files", "restore", func(j *agentpb.BackupJob) {
		j.Tag, j.Snapshot, j.TargetVolume = "policy-2", fsnaps.GetSnapshots()[0].GetId(), filesOut
	})
	if r.GetError() != "" {
		t.Fatalf("files restore: %s\n%s", r.GetError(), r.GetLog())
	}
	res, err = dc.Run(ctx, "fy-test-reader", map[string]any{
		"Image": pgImage, "Cmd": []string{"cat", "/data/note.txt"},
		"HostConfig": map[string]any{"Mounts": []map[string]any{{"Type": "volume", "Source": filesOut, "Target": "/data"}}},
	}, nil)
	if err != nil || strings.TrimSpace(res.Stdout) != "kept" {
		t.Fatalf("restored file: %v %+v", err, res)
	}
}
