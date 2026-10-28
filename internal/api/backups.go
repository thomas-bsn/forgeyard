package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/compose"
	"github.com/thomas-bsn/forgeyard/internal/nodes"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

// Backups save the named volumes of stacks, on a schedule, to a restic repository on an S3-compatible
// storage the admins set up once: off the node, encrypted, deduplicated. The node's agent runs restic in
// a short-lived container; a volume of a postgres service is saved with pg_dump instead of its files. A
// restore always goes to a new volume, which may then take the place of the old one.

const (
	settingBackupEndpoint  = "backup_s3_endpoint"
	settingBackupBucket    = "backup_s3_bucket"
	settingBackupPrefix    = "backup_s3_prefix"
	settingBackupRegion    = "backup_s3_region"
	settingBackupAccessKey = "backup_s3_access_key"
	settingBackupSecretKey = "backup_s3_secret_key"   // encrypted
	settingBackupPassword  = "backup_restic_password" // encrypted: without it, the snapshots are lost

	backupTimeout = 6 * time.Hour
	backupTick    = time.Minute
)

var bucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

// backupSettings is where the snapshots go.
type backupSettings struct {
	Endpoint  string `json:"endpoint"` // https://s3.fr-par.scw.cloud, http://minio:9000…
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix"`
	Region    string `json:"region"`
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey,omitempty"` // write only: empty keeps the current one
	SecretSet bool   `json:"secretSet"`
	// Password encrypts the repository: generated at the first save, to keep somewhere safe.
	Password   string `json:"password,omitempty"`
	Configured bool   `json:"configured"`
}

func (s *Server) loadBackupSettings(ctx context.Context) (backupSettings, error) {
	var b backupSettings
	var err error
	get := func(key string) string {
		if err != nil {
			return ""
		}
		var v string
		v, err = s.setting(ctx, s.store.Queries, key)
		return v
	}
	open := func(key string) string {
		sealed := get(key)
		if err != nil || sealed == "" {
			return ""
		}
		var v string
		v, err = s.secrets.Decrypt(sealed, key)
		return v
	}
	b.Endpoint, b.Bucket, b.Prefix, b.Region, b.AccessKey = get(settingBackupEndpoint), get(settingBackupBucket), get(settingBackupPrefix), get(settingBackupRegion), get(settingBackupAccessKey)
	b.SecretKey, b.Password = open(settingBackupSecretKey), open(settingBackupPassword)
	b.SecretSet = b.SecretKey != ""
	b.Configured = b.Endpoint != "" && b.Bucket != "" && b.AccessKey != "" && b.SecretKey != "" && b.Password != ""
	return b, err
}

// repository is restic's address of the snapshots.
func (b backupSettings) repository() string {
	repo := "s3:" + strings.TrimRight(b.Endpoint, "/") + "/" + b.Bucket
	if b.Prefix != "" {
		repo += "/" + strings.Trim(b.Prefix, "/")
	}
	return repo
}

func (b backupSettings) restic() *agentpb.Restic {
	return &agentpb.Restic{Repository: b.repository(), Password: b.Password, AccessKey: b.AccessKey, SecretKey: b.SecretKey, Region: b.Region}
}

func (s *Server) handleGetBackupSettings(w http.ResponseWriter, r *http.Request) {
	b, err := s.loadBackupSettings(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	b.SecretKey = ""
	writeJSON(w, http.StatusOK, b)
}

func (s *Server) handlePutBackupSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in backupSettings
	if !decodeJSON(w, r, &in) {
		return
	}
	cur, err := s.loadBackupSettings(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	in.Endpoint = strings.TrimRight(strings.TrimSpace(in.Endpoint), "/")
	in.Bucket, in.Prefix, in.Region = strings.TrimSpace(in.Bucket), strings.Trim(strings.TrimSpace(in.Prefix), "/"), strings.TrimSpace(in.Region)
	in.AccessKey, in.Password = strings.TrimSpace(in.AccessKey), strings.TrimSpace(in.Password)
	if u, err := url.Parse(in.Endpoint); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || (u.Path != "" && u.Path != "/") {
		writeError(w, http.StatusBadRequest, "adresse du stockage : https://… sans chemin, par exemple https://s3.fr-par.scw.cloud")
		return
	}
	if !bucketPattern.MatchString(in.Bucket) {
		writeError(w, http.StatusBadRequest, "bucket : 3 à 63 caractères parmi a-z, 0-9, . et -")
		return
	}
	if in.AccessKey == "" || (in.SecretKey == "" && !cur.SecretSet) {
		writeError(w, http.StatusBadRequest, "la clé d'accès et la clé secrète du stockage sont obligatoires")
		return
	}
	if in.Password == "" {
		in.Password = cur.Password
	}
	if in.Password == "" {
		raw := make([]byte, 24)
		rand.Read(raw)
		in.Password = base64.RawURLEncoding.EncodeToString(raw)
	}
	if len(in.Password) < 12 {
		writeError(w, http.StatusBadRequest, "mot de passe du dépôt : 12 caractères au moins")
		return
	}
	values := map[string]string{settingBackupEndpoint: in.Endpoint, settingBackupBucket: in.Bucket, settingBackupPrefix: in.Prefix,
		settingBackupRegion: in.Region, settingBackupAccessKey: in.AccessKey}
	seal := func(key, v string) bool {
		sealed, err := s.secrets.Encrypt(v, key)
		if err != nil {
			s.internalError(w, r, err)
			return false
		}
		values[key] = sealed
		return true
	}
	if in.SecretKey != "" && !seal(settingBackupSecretKey, strings.TrimSpace(in.SecretKey)) {
		return
	}
	if !seal(settingBackupPassword, in.Password) {
		return
	}
	if err := s.store.InTx(ctx, func(q *db.Queries) error { return storeSettings(ctx, q, values) }); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.logger.Info("backup storage changed", "endpoint", in.Endpoint, "bucket", in.Bucket, "by", currentUser(r).DisplayName)
	s.handleGetBackupSettings(w, r)
}

// handleTestBackupSettings opens the repository from a node (Forgeyard's own first), creating it the first
// time: it proves the storage, the keys and the password work.
func (s *Server) handleTestBackupSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b, err := s.loadBackupSettings(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !b.Configured {
		writeError(w, http.StatusConflict, "enregistrez d'abord le stockage")
		return
	}
	nodeID, ok := s.backupTestNode(ctx)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "aucun node en ligne pour tester")
		return
	}
	tctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	res, err := s.nodes.BackupJob(tctx, nodeID, &agentpb.BackupJob{Op: "snapshots", Restic: b.restic(), Tag: "forgeyard-test"})
	if err != nil {
		writeError(w, http.StatusBadGateway, "le node n'a pas répondu : "+err.Error())
		return
	}
	if res.GetError() != "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": res.GetError(), "log": res.GetLog()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) backupTestNode(ctx context.Context) (int64, bool) {
	if local, err := s.store.GetLocalNode(ctx); err == nil {
		if _, ok := s.nodes.Live(local.ID); ok {
			return local.ID, true
		}
	}
	choices, err := s.nodeChoices(ctx)
	if err != nil || len(choices) == 0 {
		return 0, false
	}
	return choices[0].ID, true
}

// backupPolicyResponse is a volume's backup schedule and how it went.
type backupPolicyResponse struct {
	ID           int64                   `json:"id"`
	Volume       string                  `json:"volume"`
	Schedule     string                  `json:"schedule"` // hourly or daily
	Hour         int64                   `json:"hour"`
	Keep         int64                   `json:"keep"`
	Enabled      bool                    `json:"enabled"`
	LastRunAt    int64                   `json:"lastRunAt,omitempty"`
	LastStatus   string                  `json:"lastStatus,omitempty"` // running, ok, error
	LastError    string                  `json:"lastError,omitempty"`
	LastSnapshot string                  `json:"lastSnapshot,omitempty"`
	NextRunAt    int64                   `json:"nextRunAt,omitempty"`
	Restores     []backupRestoreResponse `json:"restores"`
}

type backupRestoreResponse struct {
	ID        int64  `json:"id"`
	Snapshot  string `json:"snapshot"`
	Volume    string `json:"volume"`
	Status    string `json:"status"` // running, ok, error
	Error     string `json:"error,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	InUse     bool   `json:"inUse"` // the stack uses it in place of the volume
}

func (s *Server) toBackupPolicy(p db.BackupPolicy) backupPolicyResponse {
	out := backupPolicyResponse{ID: p.ID, Volume: p.Volume, Schedule: p.Schedule, Hour: p.Hour, Keep: p.Keep, Enabled: p.Enabled != 0,
		LastRunAt: p.LastRunAt, LastStatus: p.LastStatus, LastError: p.LastError, LastSnapshot: p.LastSnapshot, Restores: []backupRestoreResponse{}}
	if p.Enabled != 0 {
		out.NextRunAt = nextBackup(p, time.Now()).Unix()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	inUse := map[string]bool{}
	if st, err := s.store.GetStack(ctx, p.StackID); err == nil {
		inUse[stackOverrides(st)[p.Volume]] = true
	}
	if rows, err := s.store.ListBackupRestores(ctx, p.ID); err == nil {
		for _, r := range rows {
			out.Restores = append(out.Restores, backupRestoreResponse{ID: r.ID, Snapshot: r.Snapshot, Volume: r.Volume, Status: r.Status,
				Error: r.Error, CreatedAt: r.CreatedAt, InUse: inUse[r.Volume]})
		}
	}
	return out
}

// backupSlot is the last time a policy was due at or before now: the top of the hour, or its hour of the
// day.
func backupSlot(p db.BackupPolicy, now time.Time) time.Time {
	if p.Schedule == "hourly" {
		return time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, now.Location())
	}
	slot := time.Date(now.Year(), now.Month(), now.Day(), int(p.Hour), 0, 0, 0, now.Location())
	if now.Before(slot) {
		slot = slot.AddDate(0, 0, -1)
	}
	return slot
}

// backupDue tells whether a policy must run now: a slot passed since it last ran (or was created).
func backupDue(p db.BackupPolicy, now time.Time) bool {
	if p.Enabled == 0 || p.LastStatus == "running" {
		return false
	}
	last := time.Unix(max(p.LastRunAt, p.CreatedAt), 0)
	return last.Before(backupSlot(p, now))
}

// nextBackup is when a policy runs next.
func nextBackup(p db.BackupPolicy, now time.Time) time.Time {
	if backupDue(p, now) {
		return now
	}
	slot := backupSlot(p, now)
	if p.Schedule == "hourly" {
		return slot.Add(time.Hour)
	}
	return slot.AddDate(0, 0, 1)
}

// Backups runs the policies when they are due, until ctx ends. One job runs at a time: restic's pruning
// locks the repository.
func (s *Server) Backups(ctx context.Context) {
	// A run the server stopped in the middle is over.
	if list, err := s.store.ListBackupPolicies(ctx); err == nil {
		for _, p := range list {
			if p.LastStatus == "running" {
				s.store.FinishBackupRun(ctx, db.FinishBackupRunParams{LastStatus: "error", LastError: "interrompue par un redémarrage du server", LastSnapshot: p.LastSnapshot, ID: p.ID})
			}
		}
	}
	ticker := time.NewTicker(backupTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		list, err := s.store.ListBackupPolicies(ctx)
		if err != nil {
			s.logger.Error("listing backup policies failed", "err", err)
			continue
		}
		for _, p := range list {
			if backupDue(p, time.Now()) {
				s.runBackup(ctx, p)
			}
		}
	}
}

// backupTarget is what a policy saves: the stack's volume, and its postgres service when it has one.
type backupTarget struct {
	Stack    db.Stack
	Docker   string
	Postgres *agentpb.Postgres
	App      db.App // the service the volume belongs to, for its events
}

func (s *Server) backupTarget(ctx context.Context, p db.BackupPolicy) (backupTarget, error) {
	st, err := s.store.GetStack(ctx, p.StackID)
	if err != nil {
		return backupTarget{}, err
	}
	t := backupTarget{Stack: st, Docker: stackVolume(st, p.Volume)}
	apps, err := s.store.ListStackApps(ctx, st.ID)
	if err != nil {
		return t, err
	}
	for _, a := range apps {
		var svc compose.Service
		json.Unmarshal([]byte(a.ServiceSpec), &svc)
		for _, m := range svc.Volumes {
			if m.Kind == "bind" {
				continue
			}
			if m.Volume != p.Volume {
				continue
			}
			if t.App.ID == 0 || svc.Backup == "postgres" {
				t.App = a
			}
			if svc.Backup != "postgres" {
				continue
			}
			env, err := s.openEnv(a.EnvSealed)
			if err != nil {
				return t, err
			}
			user := env["POSTGRES_USER"]
			if user == "" {
				user = "postgres"
			}
			database := env["POSTGRES_DB"]
			if database == "" {
				database = user
			}
			t.Postgres = &agentpb.Postgres{Container: "forgeyard-app-" + strconv.FormatInt(a.ID, 10), User: user, Database: database,
				Password: env["POSTGRES_PASSWORD"], Image: a.Image, DataPath: m.Target, Pgdata: env["PGDATA"]}
		}
	}
	if t.App.ID == 0 {
		return t, errors.New("plus aucun service n'utilise le volume " + p.Volume)
	}
	return t, nil
}

// backupTag marks a volume's snapshots: they stay its own if the policy is deleted and made again.
func backupTag(p db.BackupPolicy) string {
	return "stack-" + strconv.FormatInt(p.StackID, 10) + "-" + p.Volume
}

// runBackup saves a policy's volume now, and records how it went. A failure is an event of the service
// and a notification to the admins.
func (s *Server) runBackup(ctx context.Context, p db.BackupPolicy) {
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	if err := s.store.StartBackupRun(ctx, db.StartBackupRunParams{LastRunAt: time.Now().Unix(), ID: p.ID}); err != nil {
		s.logger.Error("starting a backup failed", "policy", p.ID, "err", err)
		return
	}
	snapshot, err := s.doBackup(ctx, p)
	status, msg := "ok", ""
	if err != nil {
		status, msg, snapshot = "error", err.Error(), p.LastSnapshot
	}
	if err := s.store.FinishBackupRun(context.WithoutCancel(ctx), db.FinishBackupRunParams{LastStatus: status, LastError: msg, LastSnapshot: snapshot, ID: p.ID}); err != nil {
		s.logger.Error("recording a backup failed", "policy", p.ID, "err", err)
	}
	t, _ := s.backupTarget(ctx, p)
	if err != nil {
		s.logger.Warn("backup failed", "stack", t.Stack.Name, "volume", p.Volume, "err", err)
		if t.App.ID != 0 {
			s.appEvent(t.App.ID, eventError, "Sauvegarde du volume "+p.Volume+" en échec : "+msg)
		}
		s.notifyAdmins("backups", notification{Title: "Sauvegarde en échec : " + t.Stack.Name + " · " + p.Volume, Text: msg,
			Path: "#/projects/" + strconv.FormatInt(p.StackID, 10), Color: colorError})
		return
	}
	s.logger.Info("backup done", "stack", t.Stack.Name, "volume", p.Volume, "snapshot", snapshot)
	if t.App.ID != 0 {
		s.appEvent(t.App.ID, eventSuccess, "Volume "+p.Volume+" sauvegardé ("+snapshot+")")
	}
}

func (s *Server) doBackup(ctx context.Context, p db.BackupPolicy) (string, error) {
	b, err := s.loadBackupSettings(ctx)
	if err != nil {
		return "", err
	}
	if !b.Configured {
		return "", errors.New("le stockage des sauvegardes n'est pas configuré (Réglages › Sauvegardes)")
	}
	t, err := s.backupTarget(ctx, p)
	if err != nil {
		return "", err
	}
	if t.Postgres != nil {
		if st, ok := s.nodes.AppStatus(t.App.ID); !ok || st.Status.GetState() != "running" {
			return "", errors.New(t.App.Service + " n'est pas en ligne : pg_dump a besoin de la base qui tourne")
		}
	}
	bctx, cancel := context.WithTimeout(ctx, backupTimeout)
	defer cancel()
	res, err := s.nodes.BackupJob(bctx, t.Stack.NodeID, &agentpb.BackupJob{Op: "backup", Restic: b.restic(), Volume: t.Docker,
		Tag: backupTag(p), Keep: int32(p.Keep), Postgres: t.Postgres})
	if errors.Is(err, nodes.ErrOffline) {
		return "", errors.New("le node du projet est hors ligne")
	}
	if err != nil {
		return "", err
	}
	if res.GetError() != "" {
		return "", errors.New(res.GetError())
	}
	return res.GetSnapshot(), nil
}

// policyFromPath loads the stack of the path and the backup policy of its volume (zero when it has none).
func (s *Server) policyFromPath(w http.ResponseWriter, r *http.Request, act action) (db.Stack, db.BackupPolicy, bool) {
	st, ok := s.stackFromPath(w, r, act)
	if !ok {
		return st, db.BackupPolicy{}, false
	}
	volume := r.PathValue("volume")
	list, err := s.store.ListStackBackupPolicies(r.Context(), st.ID)
	if err != nil {
		s.internalError(w, r, err)
		return st, db.BackupPolicy{}, false
	}
	for _, p := range list {
		if p.Volume == volume {
			return st, p, true
		}
	}
	return st, db.BackupPolicy{StackID: st.ID, Volume: volume}, true
}

// handlePutBackupPolicy sets a volume's backup schedule.
func (s *Server) handlePutBackupPolicy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, p, ok := s.policyFromPath(w, r, actBackupConfig)
	if !ok {
		return
	}
	var in struct {
		Schedule string `json:"schedule"`
		Hour     int64  `json:"hour"`
		Keep     int64  `json:"keep"`
		Enabled  bool   `json:"enabled"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if !s.stackUsesVolume(ctx, st, p.Volume) {
		writeError(w, http.StatusNotFound, "aucun service du projet n'utilise ce volume")
		return
	}
	switch {
	case in.Schedule != "hourly" && in.Schedule != "daily":
		writeError(w, http.StatusBadRequest, "planning : toutes les heures (hourly) ou tous les jours (daily)")
		return
	case in.Hour < 0 || in.Hour > 23:
		writeError(w, http.StatusBadRequest, "heure : de 0 à 23")
		return
	case in.Keep < 1 || in.Keep > 365:
		writeError(w, http.StatusBadRequest, "sauvegardes gardées : de 1 à 365")
		return
	}
	b, err := s.loadBackupSettings(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !b.Configured {
		writeError(w, http.StatusConflict, "un admin doit d'abord configurer le stockage des sauvegardes (Réglages › Sauvegardes)")
		return
	}
	p, err = s.store.UpsertBackupPolicy(ctx, db.UpsertBackupPolicyParams{StackID: st.ID, Volume: p.Volume, Schedule: in.Schedule, Hour: in.Hour,
		Keep: in.Keep, Enabled: boolInt(in.Enabled), CreatedAt: time.Now().Unix()})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.logger.Info("backup policy set", "stack", st.Name, "volume", p.Volume, "schedule", p.Schedule, "keep", p.Keep, "by", currentUser(r).DisplayName)
	writeJSON(w, http.StatusOK, s.toBackupPolicy(p))
}

func (s *Server) stackUsesVolume(ctx context.Context, st db.Stack, volume string) bool {
	apps, err := s.store.ListStackApps(ctx, st.ID)
	if err != nil {
		return false
	}
	for _, a := range apps {
		var svc compose.Service
		json.Unmarshal([]byte(a.ServiceSpec), &svc)
		for _, m := range svc.Volumes {
			if m.Kind == "bind" {
				continue
			}
			if m.Volume == volume {
				return true
			}
		}
	}
	return false
}

func (s *Server) handleDeleteBackupPolicy(w http.ResponseWriter, r *http.Request) {
	_, p, ok := s.policyFromPath(w, r, actBackupConfig)
	if !ok {
		return
	}
	if p.ID != 0 {
		if err := s.store.DeleteBackupPolicy(r.Context(), p.ID); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRunBackup saves a volume now, in the background.
func (s *Server) handleRunBackup(w http.ResponseWriter, r *http.Request) {
	_, p, ok := s.policyFromPath(w, r, actBackupRun)
	if !ok {
		return
	}
	if p.ID == 0 {
		writeError(w, http.StatusNotFound, "ce volume n'a pas de sauvegarde planifiée")
		return
	}
	if p.LastStatus == "running" {
		writeError(w, http.StatusConflict, "une sauvegarde de ce volume est déjà en cours")
		return
	}
	go s.runBackup(context.Background(), p)
	w.WriteHeader(http.StatusAccepted)
}

// handleListSnapshots lists a volume's snapshots, newest first.
func (s *Server) handleListSnapshots(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, p, ok := s.policyFromPath(w, r, actView)
	if !ok {
		return
	}
	b, err := s.loadBackupSettings(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !b.Configured {
		writeError(w, http.StatusConflict, "le stockage des sauvegardes n'est pas configuré")
		return
	}
	tctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	res, err := s.nodes.BackupJob(tctx, st.NodeID, &agentpb.BackupJob{Op: "snapshots", Restic: b.restic(), Tag: backupTag(p)})
	if errors.Is(err, nodes.ErrOffline) {
		writeError(w, http.StatusServiceUnavailable, "le node du projet est hors ligne")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if res.GetError() != "" {
		writeError(w, http.StatusBadGateway, res.GetError())
		return
	}
	type snapshot struct {
		ID        string `json:"id"`
		Time      int64  `json:"time"`
		SizeBytes int64  `json:"sizeBytes"`
	}
	out := []snapshot{}
	for _, sn := range res.GetSnapshots() {
		out = append(out, snapshot{ID: sn.GetId(), Time: sn.GetTime(), SizeBytes: sn.GetSizeBytes()})
	}
	writeJSON(w, http.StatusOK, out)
}

var snapshotPattern = regexp.MustCompile(`^[0-9a-f]{8,64}$`)

// handleRestoreBackup restores a snapshot into a new volume of the stack's node, in the background. The
// volume in use is left as it is.
func (s *Server) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, p, ok := s.policyFromPath(w, r, actRestore)
	if !ok {
		return
	}
	var in struct {
		Snapshot string `json:"snapshot"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if !snapshotPattern.MatchString(in.Snapshot) {
		writeError(w, http.StatusBadRequest, "sauvegarde invalide")
		return
	}
	if p.ID == 0 {
		writeError(w, http.StatusNotFound, "ce volume n'a pas de sauvegardes")
		return
	}
	b, err := s.loadBackupSettings(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	t, err := s.backupTarget(ctx, p)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	volume := "forgeyard-stack-" + strconv.FormatInt(st.ID, 10) + "-" + p.Volume + "-" + time.Now().Format("20060102-150405")
	rs, err := s.store.CreateBackupRestore(ctx, db.CreateBackupRestoreParams{PolicyID: p.ID, Snapshot: in.Snapshot, Volume: volume, CreatedAt: time.Now().Unix()})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	user := currentUser(r).DisplayName
	go func() {
		s.backupMu.Lock()
		defer s.backupMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), backupTimeout)
		defer cancel()
		res, err := s.nodes.BackupJob(ctx, st.NodeID, &agentpb.BackupJob{Op: "restore", Restic: b.restic(), Tag: backupTag(p),
			Postgres: t.Postgres, Snapshot: in.Snapshot, TargetVolume: volume,
			TargetLabels: map[string]string{"forgeyard.stack": strconv.FormatInt(st.ID, 10), "forgeyard.volume.name": p.Volume}})
		status, msg := "ok", ""
		switch {
		case err != nil:
			status, msg = "error", err.Error()
		case res.GetError() != "":
			status, msg = "error", res.GetError()
		}
		s.store.FinishBackupRestore(ctx, db.FinishBackupRestoreParams{Status: status, Error: msg, ID: rs.ID})
		if status == "ok" {
			s.appEvent(t.App.ID, eventSuccess, fmt.Sprintf("Sauvegarde %s du volume %s restaurée dans %s par %s", in.Snapshot[:8], p.Volume, volume, user))
		} else {
			s.appEvent(t.App.ID, eventError, "Restauration du volume "+p.Volume+" en échec : "+msg)
		}
	}()
	writeJSON(w, http.StatusAccepted, backupRestoreResponse{ID: rs.ID, Snapshot: rs.Snapshot, Volume: rs.Volume, Status: rs.Status, CreatedAt: rs.CreatedAt})
}

// handleUseRestore puts a restored volume in the place of the stack's volume: its services start again on
// it. The previous volume stays on the node (deleted with the stack, if asked then).
func (s *Server) handleUseRestore(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, p, ok := s.policyFromPath(w, r, actRestore)
	if !ok {
		return
	}
	var in struct {
		Confirm bool `json:"confirm"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("restore"), 10, 64)
	rs, err := s.store.GetBackupRestore(ctx, id)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && rs.PolicyID != p.ID) {
		writeError(w, http.StatusNotFound, "restauration introuvable")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if rs.Status != "ok" {
		writeError(w, http.StatusConflict, "cette restauration n'a pas abouti")
		return
	}
	if !in.Confirm {
		writeError(w, http.StatusConflict, "confirmez : les services qui utilisent "+p.Volume+" redémarrent sur la sauvegarde restaurée")
		return
	}
	overrides := stackOverrides(st)
	overrides[p.Volume] = rs.Volume
	if err := s.store.SetStackVolumeOverrides(ctx, db.SetStackVolumeOverridesParams{VolumeOverrides: string(must(json.Marshal(overrides))), UpdatedAt: time.Now().Unix(), ID: st.ID}); err != nil {
		s.internalError(w, r, err)
		return
	}
	if t, err := s.backupTarget(ctx, p); err == nil {
		s.appEvent(t.App.ID, eventWarning, "Le volume "+p.Volume+" est maintenant "+rs.Volume+" (restauré par "+currentUser(r).DisplayName+")")
	}
	s.logger.Info("restored volume put in place", "stack", st.Name, "volume", p.Volume, "docker", rs.Volume, "by", currentUser(r).DisplayName)
	s.push(ctx, st.NodeID)
	st, err = s.store.GetStack(ctx, st.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.writeStack(w, r, http.StatusOK, st)
}
