package docker

import "testing"

func TestImageRegistry(t *testing.T) {
	for image, want := range map[string]string{
		"nginx":                       "docker.io",
		"library/nginx:1.27":          "docker.io",
		"acme/api:main":               "docker.io",
		"ghcr.io/acme/api:main":       "ghcr.io",
		"registry.example.com:5000/a": "registry.example.com:5000",
		"localhost/app":               "localhost",
	} {
		if got := ImageRegistry(image); got != want {
			t.Errorf("ImageRegistry(%q) = %q, want %q", image, got, want)
		}
	}
}

func TestSameRegistry(t *testing.T) {
	if !SameRegistry("docker.io", "index.docker.io/") || !SameRegistry("GHCR.io", "ghcr.io") {
		t.Error("the same registry under two spellings is not recognised")
	}
	if SameRegistry("ghcr.io", "docker.io") {
		t.Error("two registries are taken for one")
	}
}
