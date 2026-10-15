package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/docker"
)

// Backups go to a restic repository on an S3-compatible storage: encrypted, and deduplicated from one
// snapshot to the next. restic runs in a short-lived container that mounts the volume read-only. A
// database of a postgres service is saved with pg_dump, run in its container, its output streamed into
// restic: a consistent copy, where the files of a running database may not be.

const (
	resticImage = "restic/restic:0.17.3"
	// resticMissing is restic's exit code when the repository does not exist yet.
	resticMissing = 10
)

// runBackupJob runs one backup, listing or restore, and says how it went.
func runBackupJob(ctx context.Context, dc *docker.Client, job *agentpb.BackupJob) *agentpb.BackupResult {
	res := &agentpb.BackupResult{Id: job.GetId()}
	b := &backupRun{dc: dc, job: job, name: "forgeyard-backup-" + job.GetId()}
	var err error
	switch job.GetOp() {
	case "backup":
		err = b.backup(ctx, res)
	case "snapshots":
		err = b.snapshots(ctx, res)
	case "restore":
		err = b.restore(ctx)
	default:
		err = fmt.Errorf("opération inconnue : %s", job.GetOp())
	}
	if err != nil {
		res.Error = err.Error()
		var te *toolError
		if errors.As(err, &te) {
			res.Error, res.Log = te.msg, te.log
		}
	}
	return res
}

type backupRun struct {
	dc   *docker.Client
	job  *agentpb.BackupJob
	name string
}

// toolError is a tool that failed, with the end of what it wrote.
type toolError struct {
	msg string
	log string
}

func (e *toolError) Error() string { return e.msg }

func failed(what string, r docker.RunResult) error {
	log := strings.TrimSpace(r.Stderr)
	if log == "" {
		log = strings.TrimSpace(r.Stdout)
	}
	if len(log) > 4000 {
		log = log[len(log)-4000:]
	}
	msg := fmt.Sprintf("%s : code %d", what, r.ExitCode)
	if last := lastLine(log); last != "" {
		msg += " (" + last + ")"
	}
	return &toolError{msg: msg, log: log}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// restic runs restic with args, the volumes mounted as given (Docker volume → path), stdin fed when set.
func (b *backupRun) restic(ctx context.Context, mounts map[string]string, readOnly bool, stdin io.Reader, args ...string) (docker.RunResult, error) {
	r := b.job.GetRestic()
	env := []string{
		"RESTIC_REPOSITORY=" + r.GetRepository(), "RESTIC_PASSWORD=" + r.GetPassword(),
		"AWS_ACCESS_KEY_ID=" + r.GetAccessKey(), "AWS_SECRET_ACCESS_KEY=" + r.GetSecretKey(),
	}
	if r.GetRegion() != "" {
		env = append(env, "AWS_DEFAULT_REGION="+r.GetRegion())
	}
	var ms []map[string]any
	for vol, path := range mounts {
		ms = append(ms, map[string]any{"Type": "volume", "Source": vol, "Target": path, "ReadOnly": readOnly})
	}
	host := map[string]any{"Mounts": ms, "LogConfig": map[string]any{"Type": "json-file"}}
	if r.GetNetwork() != "" {
		host["NetworkMode"] = r.GetNetwork()
	}
	return b.dc.Run(ctx, b.name, map[string]any{
		"Image": resticImage, "Cmd": append([]string{"--no-cache"}, args...), "Env": env,
		"Labels": map[string]string{labelInternal: "backup"}, "HostConfig": host,
	}, stdin)
}

// ensureRepo creates the repository the first time.
func (b *backupRun) ensureRepo(ctx context.Context) error {
	if err := b.dc.Pull(ctx, resticImage); err != nil {
		return fmt.Errorf("téléchargement de restic : %w", err)
	}
	res, err := b.restic(ctx, nil, true, nil, "cat", "config")
	if err != nil {
		return err
	}
	if res.ExitCode == 0 {
		return nil
	}
	if res.ExitCode != resticMissing && !strings.Contains(res.Stderr, "repository does not exist") && !strings.Contains(res.Stderr, "Is there a repository") {
		return failed("accès au dépôt de sauvegardes", res)
	}
	res, err = b.restic(ctx, nil, true, nil, "init")
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return failed("création du dépôt de sauvegardes", res)
	}
	return nil
}

// backup saves the volume (or its database), then forgets the snapshots beyond the ones kept.
func (b *backupRun) backup(ctx context.Context, res *agentpb.BackupResult) error {
	if err := b.ensureRepo(ctx); err != nil {
		return err
	}
	tag := b.job.GetTag()
	var out docker.RunResult
	var err error
	if pg := b.job.GetPostgres(); pg != nil {
		out, err = b.dumpPostgres(ctx, pg)
	} else {
		out, err = b.restic(ctx, map[string]string{b.job.GetVolume(): "/data"}, true, nil,
			"backup", "/data", "--tag", tag, "--host", "forgeyard", "--json", "--quiet")
		if err == nil && out.ExitCode != 0 && out.ExitCode != 3 { // 3: some files could not be read, saved anyway
			err = failed("sauvegarde", out)
		}
	}
	if err != nil {
		return err
	}
	res.Snapshot, res.SizeBytes = backupSummary(out.Stdout)
	if keep := b.job.GetKeep(); keep > 0 {
		out, err := b.restic(ctx, nil, true, nil, "forget", "--tag", tag, "--group-by", "tags", "--keep-last", strconv.Itoa(int(keep)), "--prune")
		if err != nil {
			return err
		}
		if out.ExitCode != 0 {
			return failed("sauvegarde faite ("+res.Snapshot+"), mais le nettoyage des anciennes a échoué", out)
		}
	}
	return nil
}

// dumpPostgres streams pg_dump, run in the database's container, into restic. A dump that fails midway
// leaves no snapshot.
func (b *backupRun) dumpPostgres(ctx context.Context, pg *agentpb.Postgres) (docker.RunResult, error) {
	dump, wait, err := b.dc.ExecStream(ctx, pg.GetContainer(), []string{"pg_dump", "-Fc", "-U", pg.GetUser(), pg.GetDatabase()})
	if err != nil {
		return docker.RunResult{}, fmt.Errorf("pg_dump : %w", err)
	}
	out, err := b.restic(ctx, nil, true, dump, "backup", "--stdin", "--stdin-filename", dumpFile(pg),
		"--tag", b.job.GetTag(), "--host", "forgeyard", "--json", "--quiet")
	dump.Close()
	code, stderr, waitErr := wait()
	if err != nil {
		return out, err
	}
	snap, _ := backupSummary(out.Stdout)
	if waitErr != nil || code != 0 {
		if snap != "" {
			b.restic(context.WithoutCancel(ctx), nil, true, nil, "forget", snap)
		}
		if waitErr != nil {
			return out, fmt.Errorf("pg_dump : %w", waitErr)
		}
		return out, failed("pg_dump", docker.RunResult{ExitCode: code, Stderr: stderr})
	}
	if out.ExitCode != 0 {
		return out, failed("sauvegarde", out)
	}
	return out, nil
}

// dumpFile is the name the dump of a database has in its snapshots.
func dumpFile(pg *agentpb.Postgres) string {
	return pg.GetDatabase() + ".dump"
}

// backupSummary reads the snapshot and the size it added from restic backup --json.
func backupSummary(stdout string) (string, int64) {
	sc := bufio.NewScanner(strings.NewReader(stdout))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var m struct {
			Type      string `json:"message_type"`
			Snapshot  string `json:"snapshot_id"`
			DataAdded int64  `json:"data_added"`
		}
		if json.Unmarshal(sc.Bytes(), &m) == nil && m.Type == "summary" {
			return m.Snapshot, m.DataAdded
		}
	}
	return "", 0
}

func (b *backupRun) snapshots(ctx context.Context, res *agentpb.BackupResult) error {
	if err := b.ensureRepo(ctx); err != nil {
		return err
	}
	out, err := b.restic(ctx, nil, true, nil, "snapshots", "--tag", b.job.GetTag(), "--json")
	if err != nil {
		return err
	}
	if out.ExitCode != 0 {
		return failed("liste des sauvegardes", out)
	}
	res.Snapshots, err = parseSnapshots(out.Stdout)
	return err
}

func parseSnapshots(stdout string) ([]*agentpb.Snapshot, error) {
	var list []struct {
		ID      string    `json:"id"`
		Time    time.Time `json:"time"`
		Summary *struct {
			Processed int64 `json:"total_bytes_processed"`
		} `json:"summary"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &list); err != nil {
		return nil, fmt.Errorf("réponse de restic illisible : %w", err)
	}
	out := make([]*agentpb.Snapshot, 0, len(list))
	for i := len(list) - 1; i >= 0; i-- { // newest first
		s := &agentpb.Snapshot{Id: list[i].ID, Time: list[i].Time.Unix()}
		if list[i].Summary != nil {
			s.SizeBytes = list[i].Summary.Processed
		}
		out = append(out, s)
	}
	return out, nil
}

// restore puts a snapshot into a new volume: the files as they were, or for a database a new data
// directory of the same postgres image, where the dump is loaded with pg_restore.
func (b *backupRun) restore(ctx context.Context) error {
	if err := b.ensureRepo(ctx); err != nil {
		return err
	}
	target := b.job.GetTargetVolume()
	if vols, err := b.dc.ListVolumes(ctx, "name", target); err == nil && len(vols) > 0 {
		for _, v := range vols {
			if v.Name == target {
				return fmt.Errorf("le volume %s existe déjà : la restauration ne remplace jamais un volume", target)
			}
		}
	}
	if err := b.dc.CreateVolume(ctx, target, b.job.GetTargetLabels()); err != nil {
		return err
	}
	pg := b.job.GetPostgres()
	if pg == nil {
		out, err := b.restic(ctx, map[string]string{target: "/restore"}, false, nil, "restore", b.job.GetSnapshot()+":/data", "--target", "/restore")
		if err == nil && out.ExitCode != 0 {
			err = failed("restauration", out)
		}
		if err != nil {
			b.dc.RemoveVolume(context.WithoutCancel(ctx), target)
		}
		return err
	}
	if err := b.restorePostgres(ctx, pg, target); err != nil {
		b.dc.RemoveVolume(context.WithoutCancel(ctx), target)
		return err
	}
	return nil
}

func (b *backupRun) restorePostgres(ctx context.Context, pg *agentpb.Postgres, target string) error {
	// The dump first goes to a volume of its own, which the database container then reads.
	dumps := b.name + "-dump"
	if err := b.dc.CreateVolume(ctx, dumps, map[string]string{labelInternal: "backup"}); err != nil {
		return err
	}
	defer b.dc.RemoveVolume(context.WithoutCancel(ctx), dumps)
	out, err := b.restic(ctx, map[string]string{dumps: "/restore"}, false, nil, "restore", b.job.GetSnapshot(), "--target", "/restore")
	if err != nil {
		return err
	}
	if out.ExitCode != 0 {
		return failed("restauration du dump", out)
	}

	db := b.name + "-postgres"
	env := []string{"POSTGRES_USER=" + pg.GetUser(), "POSTGRES_DB=" + pg.GetDatabase()}
	if pg.GetPassword() != "" {
		env = append(env, "POSTGRES_PASSWORD="+pg.GetPassword())
	} else {
		env = append(env, "POSTGRES_HOST_AUTH_METHOD=trust")
	}
	if pg.GetPgdata() != "" {
		env = append(env, "PGDATA="+pg.GetPgdata())
	}
	b.dc.Remove(ctx, db)
	if err := b.dc.Create(ctx, db, map[string]any{
		"Image": pg.GetImage(), "Env": env, "Labels": map[string]string{labelInternal: "backup"},
		"HostConfig": map[string]any{"Mounts": []map[string]any{
			{"Type": "volume", "Source": target, "Target": pg.GetDataPath()},
			{"Type": "volume", "Source": dumps, "Target": "/forgeyard-restore", "ReadOnly": true},
		}},
	}); err != nil {
		return err
	}
	defer b.dc.Remove(context.WithoutCancel(ctx), db)
	if err := b.dc.Start(ctx, db); err != nil {
		return err
	}
	if err := waitPostgres(ctx, b.dc, db, pg); err != nil {
		return err
	}
	code, log, err := b.dc.ExecRun(ctx, db, []string{"pg_restore", "-U", pg.GetUser(), "-d", pg.GetDatabase(), "--no-owner",
		"--exit-on-error", "/forgeyard-restore/" + dumpFile(pg)})
	if err != nil {
		return fmt.Errorf("pg_restore : %w", err)
	}
	if code != 0 {
		return failed("pg_restore", docker.RunResult{ExitCode: code, Stderr: log})
	}
	// A clean stop, so that the data directory is consistent when the service starts on it.
	return b.dc.Stop(ctx, db)
}

// waitPostgres waits until a fresh postgres container accepts connections over TCP: while it initializes,
// its temporary server only listens on its socket.
func waitPostgres(ctx context.Context, dc *docker.Client, container string, pg *agentpb.Postgres) error {
	deadline := time.Now().Add(2 * time.Minute)
	for {
		code, log, err := dc.ExecRun(ctx, container, []string{"pg_isready", "-h", "127.0.0.1", "-U", pg.GetUser(), "-d", pg.GetDatabase()})
		if err == nil && code == 0 {
			return nil
		}
		if ct, ierr := dc.Inspect(ctx, container); ierr == nil && !ct.State.Running {
			return &toolError{msg: fmt.Sprintf("postgres s'est arrêté pendant la restauration (code %d)", ct.State.ExitCode), log: log}
		}
		if time.Now().After(deadline) {
			return &toolError{msg: "postgres n'a pas démarré en 2 minutes", log: log}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
