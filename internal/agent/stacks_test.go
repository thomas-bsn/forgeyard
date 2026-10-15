package agent

import (
	"slices"
	"strings"
	"testing"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/docker"
)

func stackApp(id int64, service string, deps ...*agentpb.Dependency) *agentpb.AppSpec {
	return &agentpb.AppSpec{Id: id, Name: "s-" + service, Image: "x", Running: true,
		Stack: &agentpb.StackService{StackId: 1, Network: "forgeyard-stack-1", Service: service, DependsOn: deps}}
}

func TestOrderAppsPutsDependenciesFirst(t *testing.T) {
	web := stackApp(1, "web", &agentpb.Dependency{AppId: 2, Service: "api", Condition: condStarted})
	api := stackApp(2, "api", &agentpb.Dependency{AppId: 4, Service: "postgres", Condition: condHealthy},
		&agentpb.Dependency{AppId: 3, Service: "migrate", Condition: condCompleted})
	migrate := stackApp(3, "migrate", &agentpb.Dependency{AppId: 4, Service: "postgres", Condition: condHealthy})
	pg := stackApp(4, "postgres")
	other := &agentpb.AppSpec{Id: 5, Name: "blog"}
	var got []int64
	for _, a := range orderApps([]*agentpb.AppSpec{web, api, migrate, pg, other}) {
		got = append(got, a.GetId())
	}
	if !slices.Equal(got, []int64{4, 3, 2, 1, 5}) {
		t.Fatalf("order: %v", got)
	}
}

func container(status string, exit int, health string) *docker.Container {
	var ct docker.Container
	ct.State.Status, ct.State.Running, ct.State.ExitCode = status, status == "running", exit
	if health != "" {
		ct.State.Health = &struct {
			Status string `json:"Status"`
		}{health}
	}
	return &ct
}

func TestDependencyReady(t *testing.T) {
	healthy := &agentpb.Dependency{Service: "postgres", Condition: condHealthy}
	done := &agentpb.Dependency{Service: "migrate", Condition: condCompleted}
	started := &agentpb.Dependency{Service: "api", Condition: condStarted}
	cases := []struct {
		name           string
		dep            *agentpb.Dependency
		ct             *docker.Container
		ready          bool
		waiting, fails string
	}{
		{"no container yet", healthy, nil, false, "démarrage de postgres", ""},
		{"starting", healthy, container("running", 0, "starting"), false, "healthy (starting)", ""},
		{"healthy", healthy, container("running", 0, "healthy"), true, "", ""},
		{"no health check", healthy, container("running", 0, ""), false, "", "pas de healthcheck"},
		{"job running", done, container("running", 0, ""), false, "fin du job migrate", ""},
		{"job done", done, container("exited", 0, ""), true, "", ""},
		{"job failed", done, container("exited", 3, ""), false, "", "a échoué (code 3)"},
		{"started", started, container("running", 0, ""), true, "", ""},
		{"started then ended", started, container("exited", 0, ""), true, "", ""},
		{"created only", started, container("created", 0, ""), false, "démarrage de api", ""},
	}
	for _, c := range cases {
		ready, waiting, failed := dependencyReady(c.dep, c.ct)
		if ready != c.ready || (c.waiting == "") != (waiting == "") || !strings.Contains(waiting, c.waiting) ||
			(c.fails == "") != (failed == "") || !strings.Contains(failed, c.fails) {
			t.Errorf("%s: ready=%v waiting=%q failed=%q", c.name, ready, waiting, failed)
		}
	}
}

func TestStackServiceContainer(t *testing.T) {
	app := stackApp(7, "postgres")
	app.Stack.Command = []string{"postgres", "-c", "fsync=off"}
	app.Stack.Healthcheck = &agentpb.Healthcheck{Test: []string{"CMD-SHELL", "pg_isready"}, IntervalNs: 5e9, Retries: 3}
	app.Stack.Volumes = []*agentpb.NamedVolume{{Name: "forgeyard-stack-1-pgdata", Target: "/var/lib/postgresql/data", Volume: "pgdata"}}
	cfg := appContainerConfig(app, "h", "proxy")
	host := cfg["HostConfig"].(map[string]any)
	if host["NetworkMode"] != "forgeyard-stack-1" || cfg["ExposedPorts"] != nil || host["PortBindings"] != nil {
		t.Fatalf("a private service is only on its stack's network, without a port: %v", host)
	}
	aliases := cfg["NetworkingConfig"].(map[string]any)["EndpointsConfig"].(map[string]any)["forgeyard-stack-1"].(map[string]any)["Aliases"]
	if !slices.Equal(aliases.([]string), []string{"postgres"}) {
		t.Fatalf("aliases: %v", aliases)
	}
	if !slices.Equal(cfg["Cmd"].([]string), app.Stack.Command) || cfg["Healthcheck"].(map[string]any)["Interval"] != int64(5e9) {
		t.Fatalf("command or health check: %v", cfg)
	}
	mounts := host["Mounts"].([]map[string]any)
	if len(mounts) != 1 || mounts[0]["Source"] != "forgeyard-stack-1-pgdata" {
		t.Fatalf("mounts: %v", mounts)
	}
	if cfg["Labels"].(map[string]string)[labelStack] != "1" {
		t.Fatal("not labeled with its stack")
	}

	// Jobs never restart; services restart unless stopped by default.
	if p := restartPolicy(&agentpb.StackService{Job: true, Restart: "always"}); p["Name"] != "no" {
		t.Fatalf("job: %v", p)
	}
	if p := restartPolicy(&agentpb.StackService{}); p["Name"] != "unless-stopped" {
		t.Fatalf("default: %v", p)
	}
	if p := restartPolicy(&agentpb.StackService{Restart: "on-failure:5"}); p["Name"] != "on-failure" || p["MaximumRetryCount"] != 5 {
		t.Fatalf("on-failure: %v", p)
	}

	// Its dependencies order its start but do not make a new version.
	before := appHash(app, "proxy")
	app.Stack.DependsOn = []*agentpb.Dependency{{AppId: 3, Service: "x", Condition: condStarted}}
	if appHash(app, "proxy") != before {
		t.Fatal("dependencies changed the spec hash")
	}
	app.Stack.Command = nil
	if appHash(app, "proxy") == before {
		t.Fatal("a new command did not change the spec hash")
	}
}

func TestJobState(t *testing.T) {
	if jobState(*container("exited", 0, "")) != "completed" || jobState(*container("exited", 1, "")) != "failed" {
		t.Fatal("job states")
	}
}
