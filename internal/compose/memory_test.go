package compose

import (
	"strings"
	"testing"
)

func TestParseMemorySize(t *testing.T) {
	for in, want := range map[string]int{
		"512m": 512, "512M": 512, "512mb": 512, "512MB": 512, "1g": 1024, "1GB": 1024, "1.5g": 1536,
		"65536k": 64, "536870912": 512, "536870913": 513, " 2g ": 2048,
	} {
		if got, ok := ParseMemorySize(in); !ok || got != want {
			t.Errorf("ParseMemorySize(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "m", "512 megs", "-1g", "1t", "1e9", "0x200m"} {
		if got, ok := ParseMemorySize(bad); ok {
			t.Errorf("ParseMemorySize(%q) = %d, want an error", bad, got)
		}
	}
}

func memoryOf(t *testing.T, src string) int {
	t.Helper()
	st, errs := Parse([]byte(src), nil)
	if errs != nil {
		t.Fatalf("parse: %v\n%s", errs, src)
	}
	return st.Services[0].MemoryMB
}

func TestParseMemoryKeys(t *testing.T) {
	for src, want := range map[string]int{
		"services:\n  web:\n    image: nginx\n":                                                                       0,
		"services:\n  web:\n    image: nginx\n    mem_limit: 768m\n":                                                  768,
		"services:\n  web:\n    image: nginx\n    mem_limit: 1073741824\n":                                            1024,
		"services:\n  web:\n    image: nginx\n    deploy:\n      resources:\n        limits:\n          memory: 2G\n": 2048,
		"services:\n  web:\n    image: nginx\n    x-forgeyard: { memory: 300 }\n":                                     300,
		// The same limit twice is fine.
		"services:\n  web:\n    image: nginx\n    mem_limit: 1g\n    x-forgeyard: { memory: 1024 }\n": 1024,
	} {
		if got := memoryOf(t, src); got != want {
			t.Errorf("got %d, want %d:\n%s", got, want, src)
		}
	}
	for src, msg := range map[string]string{
		"services:\n  web:\n    image: nginx\n    mem_limit: 512\n":                                                         "en octets",
		"services:\n  web:\n    image: nginx\n    mem_limit: 32g\n":                                                         "entre 64m et 16g",
		"services:\n  web:\n    image: nginx\n    mem_limit: lots\n":                                                        "une taille",
		"services:\n  web:\n    image: nginx\n    mem_limit: 1g\n    x-forgeyard: { memory: 512 }\n":                        "gardez une seule limite",
		"services:\n  web:\n    image: nginx\n    deploy:\n      replicas: 2\n":                                             "deploy.replicas n'est pas pris en charge",
		"services:\n  web:\n    image: nginx\n    deploy:\n      resources:\n        reservations:\n          memory: 1g\n": "deploy.resources.reservations",
	} {
		_, errs := Parse([]byte(src), nil)
		if errs == nil || !strings.Contains(errs.Error(), msg) {
			t.Errorf("want %q, got %v:\n%s", msg, errs, src)
		}
	}
}

func TestSetServiceMemory(t *testing.T) {
	cases := []struct{ name, src, service, want string }{
		{"adds mem_limit as the service's first key", `services:
  # the database
  db:
    image: postgres:17   # pinned
    environment:
      A: b
  web:
    image: nginx
`, "db", `services:
  # the database
  db:
    mem_limit: 768m
    image: postgres:17   # pinned
    environment:
      A: b
  web:
    image: nginx
`},
		{"changes mem_limit where it is, keeping its quotes and comment", "services:\n    web:\n        image: nginx\n        mem_limit: \"1g\" # enough\n", "web",
			"services:\n    web:\n        image: nginx\n        mem_limit: \"768m\" # enough\n"},
		{"changes x-forgeyard.memory in a flow mapping", "services:\n  web:\n    image: nginx\n    x-forgeyard: { expose: { port: 80 }, memory: 512 }\n", "web",
			"services:\n  web:\n    image: nginx\n    x-forgeyard: { expose: { port: 80 }, memory: 768 }\n"},
		{"changes deploy.resources.limits.memory", "services:\n  web:\n    image: nginx\n    deploy:\n      resources:\n        limits:\n          memory: 512M\n", "web",
			"services:\n  web:\n    image: nginx\n    deploy:\n      resources:\n        limits:\n          memory: 768M\n"},
		{"changes every limit of the service alike", "services:\n  web:\n    image: nginx\n    mem_limit: 1g\n    x-forgeyard:\n      memory: 1024\n", "web",
			"services:\n  web:\n    image: nginx\n    mem_limit: 768m\n    x-forgeyard:\n      memory: 768\n"},
		{"own key over an anchor's", "x-base: &base\n  mem_limit: 1g\nservices:\n  web:\n    <<: *base\n    image: nginx\n    mem_limit: 1g\n", "web",
			"x-base: &base\n  mem_limit: 1g\nservices:\n  web:\n    <<: *base\n    image: nginx\n    mem_limit: 768m\n"},
		{"non-ASCII before the value on its line", "services:\n  web: { image: nginx, command: \"écho été\", mem_limit: 512m }\n", "web",
			"services:\n  web: { image: nginx, command: \"écho été\", mem_limit: 768m }\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := SetServiceMemory([]byte(c.src), c.service, 768)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, c.want)
			}
			st, errs := Parse(got, nil)
			if errs != nil {
				t.Fatal(errs)
			}
			for _, s := range st.Services {
				if s.Name == c.service && s.MemoryMB != 768 {
					t.Fatalf("parsed memory: %d", s.MemoryMB)
				}
			}
		})
	}

	for _, c := range []struct{ name, src, service string }{
		{"unknown service", "services:\n  web:\n    image: nginx\n", "api"},
		{"from a variable", "services:\n  web:\n    image: nginx\n    mem_limit: ${WEB_MEMORY}\n", "web"},
		{"only inherited from an anchor", "x-base: &base\n  mem_limit: 1g\nservices:\n  web:\n    <<: *base\n    image: nginx\n", "web"},
		{"the whole service is an alias", "x-web: &web\n  image: nginx\nservices:\n  web: *web\n", "web"},
		{"a one-line service", "services:\n  web: { image: nginx }\n", "web"},
		{"own limit contradicting an inherited one", "x-base: &base\n  deploy: { resources: { limits: { memory: 1g } } }\nservices:\n  web:\n    <<: *base\n    image: nginx\n    mem_limit: 1g\n", "web"},
		{"invalid YAML", "services:\n  web: [\n", "web"},
	} {
		t.Run("refuses "+c.name, func(t *testing.T) {
			if got, err := SetServiceMemory([]byte(c.src), c.service, 768); err == nil {
				t.Fatalf("accepted:\n%s", got)
			}
		})
	}
	if _, err := SetServiceMemory([]byte("services:\n  web:\n    image: nginx\n"), "web", 32); err == nil {
		t.Fatal("32 Mo accepted")
	}
}
