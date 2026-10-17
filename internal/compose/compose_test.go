package compose

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// shameless is the compose file of Forgeyard's first real stack, and a docker compose one as well.
const shameless = `
name: shameless

x-db-env: &db-env
  POSTGRES_USER: shameless
  POSTGRES_DB: shameless
  POSTGRES_PASSWORD: ${DB_PASSWORD}

services:
  postgres:
    image: postgres:17-alpine
    environment:
      <<: *db-env
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U $${POSTGRES_USER}"]
      interval: 5s
      timeout: 3s
      retries: 10
    x-forgeyard:
      backup: { type: postgres }

  migrate:
    image: ghcr.io/shameless/api:${TAG:-latest}
    command: dotnet Shameless.Api.dll --migrate
    environment:
      ConnectionStrings__Default: "Host=postgres;Database=shameless;Username=shameless;Password=${DB_PASSWORD}"
    depends_on:
      postgres:
        condition: service_healthy
    restart: "no"

  api:
    image: ghcr.io/shameless/api:${TAG:-latest}
    environment:
      - ConnectionStrings__Default=Host=postgres;Database=shameless;Username=shameless;Password=${DB_PASSWORD}
      - ASPNETCORE_URLS=http://+:8080
    depends_on:
      postgres:
        condition: service_healthy
      migrate:
        condition: service_completed_successfully
    restart: unless-stopped

  web:
    image: ghcr.io/shameless/web:${TAG:-latest}
    depends_on: [api]
    x-forgeyard:
      expose: { port: 80 }

volumes:
  pgdata:
`

func TestParseShameless(t *testing.T) {
	st, errs := Parse([]byte(shameless), map[string]string{"DB_PASSWORD": "s3cret"})
	if errs != nil {
		t.Fatal(errs)
	}
	var names []string
	for _, s := range st.Services {
		names = append(names, s.Name)
	}
	// Dependencies first.
	if !slices.Equal(names, []string{"postgres", "migrate", "api", "web"}) {
		t.Fatalf("order: %v", names)
	}
	pg, migrate, api, web := st.Services[0], st.Services[1], st.Services[2], st.Services[3]
	if pg.Environment["POSTGRES_PASSWORD"] != "s3cret" || pg.Environment["POSTGRES_USER"] != "shameless" {
		t.Fatalf("merged env: %v", pg.Environment)
	}
	if pg.Healthcheck == nil || !slices.Equal(pg.Healthcheck.Test, []string{"CMD-SHELL", "pg_isready -U ${POSTGRES_USER}"}) ||
		pg.Healthcheck.Interval != 5*time.Second || pg.Healthcheck.Retries != 10 {
		t.Fatalf("healthcheck: %+v", pg.Healthcheck)
	}
	if pg.Backup != "postgres" || len(pg.Volumes) != 1 || pg.Volumes[0] != (Mount{Volume: "pgdata", Target: "/var/lib/postgresql/data"}) {
		t.Fatalf("postgres: %+v", pg)
	}
	if !migrate.Job || api.Job || pg.Job {
		t.Fatal("only migrate is a job")
	}
	if !slices.Equal(migrate.Command, []string{"dotnet", "Shameless.Api.dll", "--migrate"}) || migrate.Image != "ghcr.io/shameless/api:latest" {
		t.Fatalf("migrate: %+v", migrate)
	}
	if api.Environment["ConnectionStrings__Default"] != "Host=postgres;Database=shameless;Username=shameless;Password=s3cret" {
		t.Fatalf("api env: %v", api.Environment)
	}
	if !slices.Equal(api.DependsOn, []Dependency{{"postgres", Healthy}, {"migrate", Completed}}) {
		t.Fatalf("api deps: %v", api.DependsOn)
	}
	if web.Expose != 80 || !slices.Equal(web.DependsOn, []Dependency{{"api", Started}}) {
		t.Fatalf("web: %+v", web)
	}
	if !slices.Equal(st.Volumes, []string{"pgdata"}) {
		t.Fatalf("volumes: %v", st.Volumes)
	}
}

func TestParseRefusesWhatIsNotSupported(t *testing.T) {
	src := `services:
  app:
    build: .
    ports:
      - "8080:80"
    volumes:
      - ./data:/data
      - /var/run/docker.sock:/var/run/docker.sock
      - cache:/cache
      - /anonymous
    env_file: .env
    privileged: true
    labels: { a: b }
  other:
    image: nginx
    restart: sometimes
    depends_on: [ghost]
networks:
  default: {}
`
	_, errs := Parse([]byte(src), nil)
	if errs == nil {
		t.Fatal("accepted")
	}
	want := map[int]string{
		3:  "build n'est pas pris en charge",
		4:  "ports n'est pas pris en charge",
		7:  "pas de bind mount",
		8:  "pas de bind mount",
		9:  "doit être déclaré sous volumes:",
		10: "volume anonyme",
		11: "env_file",
		12: "privileged",
		13: "app.labels n'est pas pris en charge",
		16: "restart",
		14: "dépend de ghost",
		18: "networks",
		2:  "image est obligatoire",
	}
	for line, text := range want {
		found := false
		for _, e := range errs {
			if e.Line == line && strings.Contains(e.Message, text) {
				found = true
			}
		}
		if !found {
			t.Errorf("no error %q at line %d in:\n%s", text, line, errs)
		}
	}
	for i := 1; i < len(errs); i++ {
		if errs[i].Line < errs[i-1].Line {
			t.Fatal("errors not in line order")
		}
	}
}

func TestParseChecksServicesTogether(t *testing.T) {
	cases := map[string]string{
		"loop": `services:
  a: { image: x, depends_on: [b] }
  b: { image: x, depends_on: [a] }`,
		"two exposed": `services:
  a: { image: x, x-forgeyard: { expose: { port: 80 } } }
  b: { image: x, x-forgeyard: { expose: { port: 80 } } }`,
		"exposed job": `services:
  a: { image: x, x-forgeyard: { expose: { port: 80 } } }
  b: { image: x, depends_on: { a: { condition: service_completed_successfully } } }`,
		"restarting job": `services:
  a: { image: x, restart: always }
  b: { image: x, depends_on: { a: { condition: service_completed_successfully } } }`,
		"unknown var": `services:
  a: { image: "x:${TAG}" }`,
		"bad condition": `services:
  a: { image: x }
  b: { image: x, depends_on: { a: { condition: service_ready } } }`,
		"bad name":     "services:\n  My_App: { image: x }",
		"empty":        "",
		"bad yaml":     "services: [",
		"no services":  "volumes: {}",
		"backup type":  "services:\n  a: { image: x, x-forgeyard: { backup: { type: mysql } } }",
		"volume opts":  "services:\n  a: { image: x }\nvolumes:\n  data: { driver: nfs }",
		"tmpfs volume": "services:\n  a: { image: x, volumes: [{ type: tmpfs, target: /tmp }] }",
	}
	for name, src := range cases {
		if _, errs := Parse([]byte(src), nil); errs == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestParseKeepsServicesWhenRefused(t *testing.T) {
	// A file with an unsupported key is refused, but its services are readable: their start order is kept
	// so an editor can still preview it.
	src := `services:
  web:
    image: nginx:alpine
    build: .
    depends_on: [db]
  db:
    image: postgres:17-alpine
`
	st, errs := Parse([]byte(src), nil)
	if errs == nil {
		t.Fatal("accepted")
	}
	if st == nil {
		t.Fatal("no stack kept for a refused but readable file")
	}
	var names []string
	for _, s := range st.Services {
		names = append(names, s.Name)
	}
	if !slices.Equal(names, []string{"db", "web"}) {
		t.Fatalf("order: %v", names)
	}
	// A file nothing can be read from gives no stack.
	if st, _ := Parse([]byte("services: ["), nil); st != nil {
		t.Fatal("invalid YAML kept a stack")
	}
	if st, _ := Parse([]byte("volumes: {}"), nil); st != nil {
		t.Fatal("a file without services kept a stack")
	}
}

func TestInterpolate(t *testing.T) {
	vars := map[string]string{"A": "1", "EMPTY": ""}
	ok := map[string]string{
		"${A}":              "1",
		"$A-x":              "1-x",
		"$$A":               "$A",
		"${B:-def}":         "def",
		"${EMPTY:-def}":     "def",
		"${EMPTY-def}":      "",
		"${B-${A}}":         "1",
		"${A:+set}":         "set",
		"${B:+set}":         "",
		"price: 5$":         "price: 5$",
		"a ${A} b ${A:-} c": "a 1 b 1 c",
	}
	for in, want := range ok {
		got, err := Interpolate(in, vars)
		if err != nil || got != want {
			t.Errorf("%q: %q, %v (want %q)", in, got, err, want)
		}
	}
	for _, in := range []string{"${B}", "$B", "${B:?needed}", "${EMPTY:?}", "${A", "${A/x/y}", "${}"} {
		if _, err := Interpolate(in, vars); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestSplitWords(t *testing.T) {
	got, err := SplitWords(`sh -c 'echo "hi there"' "a b" c\ d`)
	if err != nil || !slices.Equal(got, []string{"sh", "-c", `echo "hi there"`, "a b", "c d"}) {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := SplitWords(`echo "oops`); err == nil {
		t.Fatal("unclosed quote accepted")
	}
}

func TestOrderIsStable(t *testing.T) {
	services := []Service{{Name: "c"}, {Name: "a", DependsOn: []Dependency{{"c", Started}}}, {Name: "b"}}
	got, cycle := Order(services)
	if cycle != nil {
		t.Fatal(cycle)
	}
	var names []string
	for _, s := range got {
		names = append(names, s.Name)
	}
	if !slices.Equal(names, []string{"c", "a", "b"}) {
		t.Fatalf("%v", names)
	}
}
