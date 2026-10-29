package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/auth"
	"github.com/thomas-bsn/forgeyard/internal/dns"
	"github.com/thomas-bsn/forgeyard/internal/dns/dnstest"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

const shamelessCompose = `name: shameless
services:
  postgres:
    image: postgres:17-alpine
    environment:
      POSTGRES_USER: shameless
      POSTGRES_DB: shameless
      POSTGRES_PASSWORD: ${DB_PASSWORD}
    volumes: [pgdata:/var/lib/postgresql/data]
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U shameless"]
      interval: 5s
    x-forgeyard: { backup: { type: postgres } }
  migrate:
    image: ghcr.io/shameless/api:latest
    command: ["dotnet", "Shameless.Api.dll", "--migrate"]
    depends_on: { postgres: { condition: service_healthy } }
  api:
    image: ghcr.io/shameless/api:latest
    environment: [DB_PASSWORD]
    depends_on:
      postgres: { condition: service_healthy }
      migrate: { condition: service_completed_successfully }
  web:
    image: ghcr.io/shameless/web:latest
    depends_on: [api]
    x-forgeyard: { expose: { port: 80 } }
volumes:
  pgdata:
`

// next reads the agent's stream until a message ok accepts.
func (f *fakeAgent) next(t *testing.T, ok func(*agentpb.ServerMessage) bool) *agentpb.ServerMessage {
	t.Helper()
	for {
		msg, err := f.stream.Recv()
		if err != nil {
			t.Fatal(err)
		}
		if ok(msg) {
			return msg
		}
	}
}

// latestDesired reads desired states until one with n apps comes.
func (f *fakeAgent) desiredWith(t *testing.T, n int) map[string]*agentpb.AppSpec {
	t.Helper()
	for {
		d := f.desired(t)
		if len(d.GetApps()) == n {
			out := map[string]*agentpb.AppSpec{}
			for _, a := range d.GetApps() {
				out[a.GetStack().GetService()] = a
			}
			return out
		}
	}
}

func setupStackServer(t *testing.T) (string, *Server, *http.Client, *fakeAgent) {
	t.Helper()
	ts, server := newTestServerWithHandle(t)
	admin := newClient()
	post(t, admin, ts.URL+"/api/setup", setupRequest{Token: testToken, InstanceName: "F", Username: "boss",
		Password: "a-long-enough-password", PublicURL: "https://forgeyard.example.com"})
	mem := dnstest.New("example.com.")
	server.newDNSProvider = func(string, map[string]string) (dns.Provider, error) { return mem, nil }
	if code := put(t, admin, ts.URL+"/api/admin/settings/domain", putDomainSettings{
		PublicURL: "https://forgeyard.example.com", Mode: "provider", Domain: "example.com", PublicIP: "203.0.113.1",
		Provider: "cloudflare", Credentials: map[string]string{"api_token": "x"},
	}); code != http.StatusOK {
		t.Fatalf("domain settings: %d", code)
	}
	fa := connectFakeAgent(t, ts.URL, server, admin, "nas")
	fa.desired(t)
	return ts.URL, server, admin, fa
}

func TestStackPreviewListsHostFolders(t *testing.T) {
	url, _, admin, _ := setupStackServer(t)
	var preview stackPreview
	compose := "services:\n  media:\n    image: jellyfin/jellyfin\n    volumes:\n      - /srv/media:/media:ro\n      - config:/config\nvolumes:\n  config: {}\n"
	if code := postJSON(t, admin, url+"/api/stacks/validate", stackInput{Name: "media", Compose: compose}, &preview); code != http.StatusOK || len(preview.Errors) != 0 {
		t.Fatalf("preview: %d %+v", code, preview)
	}
	want := stackPreviewBind{Service: "media", Source: "/srv/media", Target: "/media", ReadOnly: true}
	if len(preview.Binds) != 1 || preview.Binds[0] != want {
		t.Fatalf("binds: %+v", preview.Binds)
	}
	// The right-hand preview lists what each service mounts, named volumes included.
	if len(preview.Services) != 1 || len(preview.Services[0].Mounts) != 2 {
		t.Fatalf("mounts: %+v", preview.Services)
	}
	if m := preview.Services[0].Mounts[0]; m.Kind != "bind" || m.Source != "/srv/media" || m.Target != "/media" || !m.ReadOnly {
		t.Fatalf("bind mount: %+v", m)
	}
	if m := preview.Services[0].Mounts[1]; m.Kind != "" || m.Volume != "config" || m.Target != "/config" {
		t.Fatalf("named volume: %+v", m)
	}
}

func TestStackLifecycle(t *testing.T) {
	url, _, admin, fa := setupStackServer(t)

	// A compose file Forgeyard cannot run is refused with each problem and its line.
	var bad stackErrors
	if code := postJSON(t, admin, url+"/api/stacks", stackInput{Name: "bad", Compose: "services:\n  app:\n    build: .\n    ports: [\"80:80\"]\n"}, &bad); code != http.StatusBadRequest {
		t.Fatalf("unsupported compose: %d", code)
	}
	if len(bad.Errors) != 3 || bad.Errors[0].Line != 2 {
		t.Fatalf("errors: %+v", bad.Errors)
	}
	// A missing variable too.
	if code := postJSON(t, admin, url+"/api/stacks", stackInput{Name: "shameless", Compose: shamelessCompose}, &bad); code != http.StatusBadRequest ||
		!strings.Contains(bad.Errors[0].Message, "DB_PASSWORD") {
		t.Fatalf("missing variable: %d %+v", code, bad.Errors)
	}
	// Validation alone saves nothing.
	var preview stackPreview
	if code := postJSON(t, admin, url+"/api/stacks/validate", stackInput{Name: "shameless", Compose: shamelessCompose, Env: map[string]string{"DB_PASSWORD": "pw"}}, &preview); code != http.StatusOK ||
		len(preview.Errors) != 0 || len(preview.Services) != 4 || preview.Services[3].App != "shameless" || preview.Services[1].Kind != kindJob {
		t.Fatalf("preview: %d %+v", code, preview)
	}

	var st stackResponse
	if code := postJSON(t, admin, url+"/api/stacks", stackInput{Name: "shameless", Compose: shamelessCompose, Env: map[string]string{"DB_PASSWORD": "pw"}}, &st); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	if st.URL != "https://shameless.example.com" || len(st.Services) != 4 || st.Services[0].Service != "postgres" || st.Services[3].Name != "shameless" {
		t.Fatalf("stack: %+v", st)
	}
	if len(st.Volumes) != 1 || st.Volumes[0].Postgres != "postgres" || st.Volumes[0].Docker != "forgeyard-stack-"+itoa(st.ID)+"-pgdata" {
		t.Fatalf("volumes: %+v", st.Volumes)
	}
	apps := fa.desiredWith(t, 4)
	web, api, migrate, pg := apps["web"], apps["api"], apps["migrate"], apps["postgres"]
	network := "forgeyard-stack-" + itoa(st.ID)
	if web.GetHostname() != "shameless.example.com" || web.GetPort() != 80 || web.GetStack().GetNetwork() != network {
		t.Fatalf("web: %+v", web)
	}
	if pg.GetHostname() != "" || pg.GetPort() != 0 || pg.GetStack().GetVolumes()[0].GetName() != network+"-pgdata" || pg.GetStack().GetHealthcheck() == nil {
		t.Fatalf("postgres: %+v", pg)
	}
	if !migrate.GetStack().GetJob() || api.GetStack().GetJob() || len(migrate.GetStack().GetCommand()) != 3 {
		t.Fatalf("migrate: %+v", migrate)
	}
	deps := api.GetStack().GetDependsOn()
	if len(deps) != 2 || deps[0].GetAppId() != pg.GetId() || deps[0].GetCondition() != "service_healthy" ||
		deps[1].GetAppId() != migrate.GetId() || deps[1].GetCondition() != "service_completed_successfully" {
		t.Fatalf("api depends on: %v", deps)
	}
	if api.GetEnv()["DB_PASSWORD"] != "pw" || pg.GetEnv()["POSTGRES_PASSWORD"] != "pw" {
		t.Fatalf("variables: %v %v", api.GetEnv(), pg.GetEnv())
	}

	// Names are taken: the stack's, and those of its services.
	if code := postJSON(t, admin, url+"/api/stacks", stackInput{Name: "shameless", Compose: "services:\n  x: { image: nginx }\n"}, nil); code != http.StatusConflict {
		t.Fatalf("same stack name: %d", code)
	}
	if code := postJSON(t, admin, url+"/api/apps", appInput{Name: "shameless-api", Image: "nginx", Port: 80}, nil); code != http.StatusConflict {
		t.Fatalf("app with a service's name: %d", code)
	}
	// A service is changed through its stack only.
	if code := putJSON(t, admin, url+"/api/apps/"+itoa(api.GetId()), appInput{Image: "nginx", Port: 80}, nil); code != http.StatusConflict {
		t.Fatalf("editing a service: %d", code)
	}
	if code := del(t, admin, url+"/api/apps/"+itoa(api.GetId())); code != http.StatusConflict {
		t.Fatalf("deleting a service: %d", code)
	}

	// A new compose file only deploys the services that changed.
	changed := strings.Replace(shamelessCompose, "ghcr.io/shameless/web:latest", "ghcr.io/shameless/web:v2", 1)
	if code := putJSON(t, admin, url+"/api/stacks/"+itoa(st.ID), stackInput{Compose: changed, Env: map[string]string{"DB_PASSWORD": "pw"}}, &st); code != http.StatusOK {
		t.Fatalf("update: %d", code)
	}
	after := fa.desiredWith(t, 4)
	for name, a := range apps {
		bumped := after[name].GetGeneration() != a.GetGeneration()
		if bumped != (name == "web") {
			t.Fatalf("%s redeployed: %v", name, bumped)
		}
	}
	// A variable reaches the services that use it.
	if code := putJSON(t, admin, url+"/api/stacks/"+itoa(st.ID), stackInput{Compose: changed, Env: map[string]string{"DB_PASSWORD": "new"}}, &st); code != http.StatusOK {
		t.Fatalf("update variables: %d", code)
	}
	again := fa.desiredWith(t, 4)
	if again["web"].GetGeneration() != after["web"].GetGeneration() || again["api"].GetGeneration() == after["api"].GetGeneration() || again["api"].GetEnv()["DB_PASSWORD"] != "new" {
		t.Fatal("only the services using the variable are redeployed")
	}

	// Stopping stops every service.
	if code := postJSON(t, admin, url+"/api/stacks/"+itoa(st.ID)+"/stop", struct{}{}, &st); code != http.StatusOK || st.State != "stopped" {
		t.Fatalf("stop: %d %s", code, st.State)
	}
	for _, a := range fa.desiredWith(t, 4) {
		if a.GetRunning() {
			t.Fatalf("%s still running", a.GetName())
		}
	}

	// Deleting the stack deletes its apps; its volumes only when confirmed.
	if code := del(t, admin, url+"/api/stacks/"+itoa(st.ID)+"?volumes=1"); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
	msg := fa.next(t, func(m *agentpb.ServerMessage) bool { return m.GetDropStack() != nil })
	if msg.GetDropStack().GetStackId() != st.ID || !msg.GetDropStack().GetVolumes() {
		t.Fatalf("drop: %v", msg.GetDropStack())
	}
	var list []appResponse
	get(t, admin, url+"/api/apps", &list)
	if len(list) != 0 {
		t.Fatalf("apps left: %d", len(list))
	}
}

func TestComposeBindMountsCannotBypassApprovalOnCreateOrUpdate(t *testing.T) {
	url, server, admin, _ := setupStackServer(t)
	bindCompose := "services:\n  web:\n    image: nginx:alpine\n    volumes:\n      - /srv/site:/site\n"
	if code := postJSON(t, admin, url+"/api/stacks", stackInput{Name: "unapproved", Compose: bindCompose}, nil); code != http.StatusBadRequest {
		t.Fatalf("direct bind create: %d", code)
	}
	if taken, err := server.store.NameTaken(context.Background(), "unapproved"); err != nil || taken != 0 {
		t.Fatalf("direct create staged resources: %d %v", taken, err)
	}
	var stack stackResponse
	if code := postJSON(t, admin, url+"/api/stacks", stackInput{Name: "editable", Compose: "services:\n  web: { image: nginx:alpine }\n"}, &stack); code != http.StatusCreated {
		t.Fatalf("create baseline: %d", code)
	}
	if code := putJSON(t, admin, url+"/api/stacks/"+itoa(stack.ID), stackInput{Compose: bindCompose}, nil); code != http.StatusBadRequest {
		t.Fatalf("direct bind update: %d", code)
	}
	stored, err := server.store.GetStack(context.Background(), stack.ID)
	if err != nil || strings.Contains(stored.Compose, "/srv/site") {
		t.Fatalf("rejected update changed saved compose: %+v %v", stored, err)
	}
}

// TestStackReadOnlyForMembers: a member opens another member's project from a profile, read-only, and sees
// only the apps its owner shows, never its compose file, its variables or its volumes.
func TestStackReadOnlyForMembers(t *testing.T) {
	url, server, admin, _ := setupStackServer(t)
	var st stackResponse
	if code := postJSON(t, admin, url+"/api/stacks", stackInput{Name: "shameless", Compose: shamelessCompose, Env: map[string]string{"DB_PASSWORD": "pw"}}, &st); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	hash, _ := auth.HashPassword("another-long-password")
	if _, err := server.store.CreateLocalUser(context.Background(), db.CreateLocalUserParams{
		Username: nullString("lea"), PasswordHash: nullString(hash), DisplayName: "Léa", Role: "user", CreatedAt: time.Now().Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	lea := newClient()
	if resp := post(t, lea, url+"/api/auth/login", loginRequest{Username: "lea", Password: "another-long-password"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %d", resp.StatusCode)
	}

	var view stackResponse
	get(t, lea, url+"/api/stacks/"+itoa(st.ID), &view)
	if len(view.Services) != 4 || view.URL != "https://shameless.example.com" || len(view.Volumes) != 0 || view.Compose != "" {
		t.Fatalf("read-only view: %+v", view)
	}
	var raw map[string]any
	get(t, lea, url+"/api/stacks/"+itoa(st.ID), &raw)
	if _, ok := raw["env"]; ok || strings.Contains(fmt.Sprint(raw), "DB_PASSWORD") {
		t.Fatalf("leaks the project's variables: %v", raw)
	}

	// What the owner hides leaves the view, address included.
	byService := map[string]appResponse{}
	for _, s := range st.Services {
		byService[s.Service] = s
	}
	for _, name := range []string{"postgres", "api", "migrate"} {
		putJSON(t, admin, url+"/api/apps/"+itoa(byService[name].ID)+"/public", map[string]bool{"public": false}, nil)
	}
	view = stackResponse{}
	get(t, lea, url+"/api/stacks/"+itoa(st.ID), &view)
	if len(view.Services) != 1 || view.Services[0].Service != "web" || view.URL != "https://shameless.example.com" {
		t.Fatalf("view with hidden services: %+v", view)
	}
	putJSON(t, admin, url+"/api/apps/"+itoa(byService["web"].ID)+"/public", map[string]bool{"public": false}, nil)
	view = stackResponse{}
	get(t, lea, url+"/api/stacks/"+itoa(st.ID), &view)
	if len(view.Services) != 0 || view.URL != "" {
		t.Fatalf("view with everything hidden: %+v", view)
	}

	// Reading is all a member may do.
	if code := putJSON(t, lea, url+"/api/stacks/"+itoa(st.ID), stackInput{Compose: shamelessCompose}, nil); code != http.StatusNotFound {
		t.Fatalf("member edited a project: %d", code)
	}
	if code := postJSON(t, lea, url+"/api/stacks/"+itoa(st.ID)+"/stop", struct{}{}, nil); code != http.StatusNotFound {
		t.Fatalf("member stopped a project: %d", code)
	}
	if code := del(t, lea, url+"/api/stacks/"+itoa(st.ID)); code != http.StatusNotFound {
		t.Fatalf("member deleted a project: %d", code)
	}
}

func TestDiffStack(t *testing.T) {
	app := func(id int64, service, name, kind, image string) stackApp {
		return stackApp{App: db.App{ID: id, Service: service, Name: name, Kind: kind, Image: image, Port: 0, MemoryMb: 512, ServiceSpec: "{}"}, Env: map[string]string{"A": "1"}}
	}
	want := func(service, name, kind, image string, env map[string]string) plannedApp {
		return plannedApp{Service: service, Name: name, Kind: kind, Image: image, MemoryMB: 512, Spec: "{}", Env: env}
	}
	existing := []stackApp{
		app(1, "db", "s-db", kindService, "postgres:17"),
		app(2, "api", "s-api", kindService, "api:1"),
		app(3, "web", "s", kindWeb, "web:1"),
		app(4, "old", "s-old", kindService, "x:1"),
		app(5, "cache", "s-cache", kindService, "redis:7"),
	}
	plan := diffStack(existing, []plannedApp{
		want("db", "s-db", kindService, "postgres:17", map[string]string{"A": "1"}), // same
		want("api", "s-api", kindService, "api:2", map[string]string{"A": "1"}),     // new image
		want("web", "s", kindWeb, "web:1", map[string]string{"A": "2"}),             // new variable
		want("cache", "s-cache", kindWeb, "redis:7", map[string]string{"A": "1"}),   // now exposed: renamed
		want("new", "s-new", kindJob, "job:1", nil),
	})
	ids := func(apps []db.App) (out []int64) {
		for _, a := range apps {
			out = append(out, a.ID)
		}
		return out
	}
	if got := ids(plan.Same); len(got) != 1 || got[0] != 1 {
		t.Fatalf("same: %v", got)
	}
	if len(plan.Update) != 2 || plan.Update[0].App.ID != 2 || plan.Update[1].App.ID != 3 {
		t.Fatalf("update: %+v", plan.Update)
	}
	if got := ids(plan.Remove); len(got) != 2 || got[0] != 5 || got[1] != 4 {
		t.Fatalf("remove: %v", got)
	}
	if len(plan.Create) != 2 || plan.Create[0].Service != "cache" || plan.Create[1].Service != "new" {
		t.Fatalf("create: %+v", plan.Create)
	}
}

func TestBackupSchedule(t *testing.T) {
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	created := at("2026-10-15 10:30").Unix()
	daily := db.BackupPolicy{Schedule: "daily", Hour: 3, Keep: 7, Enabled: 1, CreatedAt: created}
	hourly := db.BackupPolicy{Schedule: "hourly", Keep: 24, Enabled: 1, CreatedAt: created}
	cases := []struct {
		p    db.BackupPolicy
		last string
		now  string
		due  bool
	}{
		{daily, "", "2026-10-15 23:59", false}, // created after today's 3:00: waits for tomorrow's
		{daily, "", "2026-10-16 03:00", true},  // tomorrow's slot
		{daily, "2026-10-16 03:00", "2026-10-16 22:00", false},
		{daily, "2026-10-16 03:00", "2026-10-17 02:59", false},
		{daily, "2026-10-16 03:00", "2026-10-18 09:00", true}, // the server was down at 3:00: it catches up once
		{hourly, "", "2026-10-15 10:59", false},
		{hourly, "", "2026-10-15 11:00", true},
		{hourly, "2026-10-15 11:00", "2026-10-15 11:40", false},
		{hourly, "2026-10-15 11:00", "2026-10-15 12:01", true},
	}
	for i, c := range cases {
		p := c.p
		if c.last != "" {
			p.LastRunAt = at(c.last).Unix()
		}
		if got := backupDue(p, at(c.now)); got != c.due {
			t.Errorf("case %d: due=%v", i, got)
		}
	}
	if next := nextBackup(daily, at("2026-10-15 12:00")); !next.Equal(at("2026-10-16 03:00")) {
		t.Fatalf("next daily: %v", next)
	}
	if next := nextBackup(hourly, at("2026-10-15 10:40")); !next.Equal(at("2026-10-15 11:00")) {
		t.Fatalf("next hourly: %v", next)
	}
	// Turned off, running already: not due.
	off := daily
	off.Enabled = 0
	running := daily
	running.LastStatus = "running"
	for _, p := range []db.BackupPolicy{off, running} {
		if backupDue(p, at("2026-10-20 05:00")) {
			t.Fatal("due while off or running")
		}
	}
}

func TestStackBackups(t *testing.T) {
	url, server, admin, fa := setupStackServer(t)
	var st stackResponse
	if code := postJSON(t, admin, url+"/api/stacks", stackInput{Name: "shameless", Compose: shamelessCompose, Env: map[string]string{"DB_PASSWORD": "pw"}}, &st); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	apps := fa.desiredWith(t, 4)
	policy := map[string]any{"schedule": "daily", "hour": 3, "keep": 7, "enabled": true}

	// Without a storage, no policy.
	if code := putJSON(t, admin, url+"/api/stacks/"+itoa(st.ID)+"/backups/pgdata", policy, nil); code != http.StatusConflict {
		t.Fatalf("policy without storage: %d", code)
	}
	var settings backupSettings
	if code := putJSON(t, admin, url+"/api/admin/settings/backups", backupSettings{Endpoint: "ftp://x", Bucket: "b", AccessKey: "a", SecretKey: "s"}, nil); code != http.StatusBadRequest {
		t.Fatalf("bad endpoint: %d", code)
	}
	if code := putJSON(t, admin, url+"/api/admin/settings/backups", backupSettings{Endpoint: "https://s3.example.com/", Bucket: "forgeyard-backups", Prefix: "/prod/", AccessKey: "AK", SecretKey: "SK"}, &settings); code != http.StatusOK {
		t.Fatalf("storage: %d", code)
	}
	if !settings.Configured || len(settings.Password) < 20 || settings.SecretKey != "" || !settings.SecretSet {
		t.Fatalf("settings: %+v", settings)
	}
	stored, _ := server.loadBackupSettings(t.Context())
	if stored.repository() != "s3:https://s3.example.com/forgeyard-backups/prod" {
		t.Fatalf("repository: %s", stored.repository())
	}

	if code := putJSON(t, admin, url+"/api/stacks/"+itoa(st.ID)+"/backups/nope", policy, nil); code != http.StatusNotFound {
		t.Fatalf("unknown volume: %d", code)
	}
	var p backupPolicyResponse
	if code := putJSON(t, admin, url+"/api/stacks/"+itoa(st.ID)+"/backups/pgdata", policy, &p); code != http.StatusOK || p.NextRunAt == 0 {
		t.Fatalf("policy: %d %+v", code, p)
	}

	// pg_dump needs the database running: the node says it is.
	fa.stream.Send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_AppStatuses{AppStatuses: &agentpb.AppStatuses{
		Apps: []*agentpb.AppStatus{{AppId: apps["postgres"].GetId(), State: "running"}}}}})
	time.Sleep(100 * time.Millisecond)

	if code := postJSON(t, admin, url+"/api/stacks/"+itoa(st.ID)+"/backups/pgdata/run", struct{}{}, nil); code != http.StatusAccepted {
		t.Fatalf("run: %d", code)
	}
	job := fa.next(t, func(m *agentpb.ServerMessage) bool { return m.GetBackupJob() != nil }).GetBackupJob()
	pg := job.GetPostgres()
	if job.GetOp() != "backup" || job.GetKeep() != 7 || job.GetVolume() != "forgeyard-stack-"+itoa(st.ID)+"-pgdata" || job.GetTag() != "stack-"+itoa(st.ID)+"-pgdata" ||
		pg.GetUser() != "shameless" || pg.GetDatabase() != "shameless" || pg.GetContainer() != "forgeyard-app-"+itoa(apps["postgres"].GetId()) ||
		job.GetRestic().GetSecretKey() != "SK" {
		t.Fatalf("job: %+v", job)
	}
	// It fails: the policy says so, and the service's events too.
	fa.stream.Send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_BackupResult{BackupResult: &agentpb.BackupResult{Id: job.GetId(), Error: "pg_dump : code 1"}}})
	deadline := time.Now().Add(5 * time.Second)
	for {
		get(t, admin, url+"/api/stacks/"+itoa(st.ID), &st)
		if b := st.Volumes[0].Backup; b != nil && b.LastStatus == "error" {
			if b.LastError != "pg_dump : code 1" {
				t.Fatalf("error: %q", b.LastError)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("backup never finished: %+v", st.Volumes[0].Backup)
		}
		time.Sleep(50 * time.Millisecond)
	}
	var events []appEventResponse
	get(t, admin, url+"/api/apps/"+itoa(apps["postgres"].GetId())+"/events", &events)
	if len(events) == 0 || !strings.Contains(events[0].Message, "Sauvegarde du volume pgdata en échec") {
		t.Fatalf("events: %+v", events)
	}
}

func TestStackIDsAreNotReused(t *testing.T) {
	url, _, admin, _ := setupStackServer(t)
	compose := "services:\n  db: { image: postgres, volumes: [data:/data] }\nvolumes:\n  data:\n"
	var first, second stackResponse
	if code := postJSON(t, admin, url+"/api/stacks", stackInput{Name: "one", Compose: compose}, &first); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	// Deleted without its volumes: they stay on the node, named after its id.
	if code := del(t, admin, url+"/api/stacks/"+itoa(first.ID)); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
	if code := postJSON(t, admin, url+"/api/stacks", stackInput{Name: "two", Compose: compose}, &second); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	if second.ID == first.ID || second.Volumes[0].Docker == first.Volumes[0].Docker {
		t.Fatalf("the new stack would get the old one's volume: %s", second.Volumes[0].Docker)
	}
}

func TestStackRegistryLogin(t *testing.T) {
	url, _, admin, fa := setupStackServer(t)
	compose := "services:\n  web:\n    image: ghcr.io/acme/private:main\n    x-forgeyard: { expose: { port: 80 } }\n"
	reg := &registryInput{Server: "https://GHCR.io/", Username: "acme", Password: "s3cret"}
	var st stackResponse
	if code := postJSON(t, admin, url+"/api/stacks", stackInput{Name: "private", Compose: compose, Registry: reg}, &st); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	if st.Registry == nil || st.Registry.Server != "ghcr.io" || st.Registry.Username != "acme" || !st.Registry.PasswordSet {
		t.Fatalf("registry: %+v", st.Registry)
	}
	web := fa.desiredWith(t, 1)["web"].GetStack()
	if web.GetRegistryServer() != "ghcr.io" || web.GetRegistryUsername() != "acme" || web.GetRegistryPassword() != "s3cret" {
		t.Fatalf("what the agent gets: %+v", web)
	}

	// Saving without a password keeps the one saved; a login without any is refused.
	same := &registryInput{Server: "ghcr.io", Username: "acme2"}
	if code := putJSON(t, admin, url+"/api/stacks/"+itoa(st.ID), stackInput{Compose: compose, Registry: same}, &st); code != http.StatusOK {
		t.Fatalf("update: %d", code)
	}
	if web = fa.desiredWith(t, 1)["web"].GetStack(); web.GetRegistryUsername() != "acme2" || web.GetRegistryPassword() != "s3cret" {
		t.Fatalf("password kept: %+v", web)
	}
	if code := putJSON(t, admin, url+"/api/stacks/"+itoa(st.ID), stackInput{Compose: compose, Registry: &registryInput{Server: "docker.io", Username: "x"}}, nil); code != http.StatusBadRequest {
		t.Fatalf("a login without a password: %d", code)
	}

	// An update that does not mention it leaves it; an empty server removes it.
	if code := putJSON(t, admin, url+"/api/stacks/"+itoa(st.ID), stackInput{Compose: compose}, &st); code != http.StatusOK || st.Registry == nil {
		t.Fatalf("untouched: %d %+v", code, st.Registry)
	}
	var removed stackResponse
	if code := putJSON(t, admin, url+"/api/stacks/"+itoa(st.ID), stackInput{Compose: compose, Registry: &registryInput{}}, &removed); code != http.StatusOK || removed.Registry != nil {
		t.Fatalf("removed: %d %+v", code, removed.Registry)
	}
}

func TestStackServiceUser(t *testing.T) {
	url, _, admin, fa := setupStackServer(t)
	compose := func(user string) string {
		return "services:\n  web:\n    image: nginx\n    user: " + user + "\n    x-forgeyard: { expose: { port: 80 } }\n"
	}
	var st stackResponse
	if code := postJSON(t, admin, url+"/api/stacks", stackInput{Name: "runas", Compose: compose("1000:1000")}, &st); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	web := fa.desiredWith(t, 1)["web"]
	if web.GetStack().GetUser() != "1000:1000" {
		t.Fatalf("what the agent gets: %q", web.GetStack().GetUser())
	}
	// Another user redeploys the service.
	if code := putJSON(t, admin, url+"/api/stacks/"+itoa(st.ID), stackInput{Compose: compose("www-data")}, &st); code != http.StatusOK {
		t.Fatalf("update: %d", code)
	}
	after := fa.desiredWith(t, 1)["web"]
	if after.GetStack().GetUser() != "www-data" || after.GetGeneration() == web.GetGeneration() {
		t.Fatalf("redeploy: %q, generation %d → %d", after.GetStack().GetUser(), web.GetGeneration(), after.GetGeneration())
	}
	var bad stackErrors
	if code := postJSON(t, admin, url+"/api/stacks", stackInput{Name: "badrun", Compose: compose(`"a:b:c"`)}, &bad); code != http.StatusBadRequest || len(bad.Errors) != 1 || bad.Errors[0].Line != 4 {
		t.Fatalf("bad user: %d %+v", code, bad)
	}
}
