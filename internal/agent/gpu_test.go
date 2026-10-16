package agent

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
)

func TestAppWithoutGpuIsUnchanged(t *testing.T) {
	app := &agentpb.AppSpec{Id: 3, Name: "ollama", Image: "ollama/ollama:latest", Port: 11434, Hostname: "ollama.example.com", Running: true, Generation: 2}
	cfg := appContainerConfig(app, "h", "traefik")
	if _, ok := cfg["HostConfig"].(map[string]any)["DeviceRequests"]; ok {
		t.Fatal("an app without a GPU got DeviceRequests")
	}
	// Its hash is the one agents computed before GPUs existed: its container is not recreated.
	before := specHash(app.GetImage(), strconv.Itoa(int(app.GetPort())), "", app.GetHostname(), "0", "2", "traefik", "false", "", "")
	if appHash(app, "traefik") != before {
		t.Fatal("the hash of an app without a GPU changed")
	}
}

func TestGpuRequests(t *testing.T) {
	app := &agentpb.AppSpec{Id: 3, Name: "ollama", Image: "ollama/ollama", Port: 11434, Running: true}
	noGpu := appHash(app, "proxy")

	app.Gpu = &agentpb.GpuRequest{Vendor: vendorNvidia, All: true}
	req := appContainerConfig(app, "h", "proxy")["HostConfig"].(map[string]any)["DeviceRequests"].([]map[string]any)
	if len(req) != 1 || req[0]["Driver"] != "nvidia" || req[0]["Count"] != -1 || req[0]["DeviceIDs"] != nil {
		t.Fatalf("all GPUs: %v", req)
	}
	all := appHash(app, "proxy")
	if all == noGpu {
		t.Fatal("asking for a GPU did not make a new container")
	}

	app.Gpu = &agentpb.GpuRequest{Vendor: vendorNvidia, DeviceIds: []string{"GPU-1a2b"}}
	req = appContainerConfig(app, "h", "proxy")["HostConfig"].(map[string]any)["DeviceRequests"].([]map[string]any)
	if _, counted := req[0]["Count"]; counted || !slices.Equal(req[0]["DeviceIDs"].([]string), []string{"GPU-1a2b"}) {
		t.Fatalf("one GPU: %v", req)
	}
	if appHash(app, "proxy") == all {
		t.Fatal("picking one GPU instead of all did not make a new container")
	}

	// A sandbox gets its GPU too.
	app.Sandbox = true
	if _, ok := appContainerConfig(app, "h", "proxy")["HostConfig"].(map[string]any)["DeviceRequests"]; !ok {
		t.Fatal("sandbox without its GPU")
	}
}

func TestParseNvidiaSmi(t *testing.T) {
	out := "0, GPU-5f3c1a2b-0000-1111-2222-333344445555, NVIDIA GeForce RTX 4070 SUPER, 12282\n1, GPU-aaaa, NVIDIA RTX A2000, 6138\nbroken line\n"
	gpus := parseNvidiaSmi(out)
	if len(gpus) != 2 || gpus[0].GetName() != "NVIDIA GeForce RTX 4070 SUPER" || gpus[0].GetMemoryBytes() != 12282<<20 ||
		gpus[0].GetId() != "GPU-5f3c1a2b-0000-1111-2222-333344445555" || gpus[1].GetIndex() != 1 || gpus[0].GetVendor() != "nvidia" {
		t.Fatalf("%v", gpus)
	}
	if parseNvidiaSmi("") != nil {
		t.Fatal("no GPU expected")
	}
}

func TestGpuErrors(t *testing.T) {
	refused := errors.New(`docker POST /containers/x/start: 500 could not select device driver "nvidia" with capabilities: [[gpu]]`)
	if !noGpuSupport(refused) || !strings.Contains(gpuStartError(refused).Error(), "NVIDIA Container Toolkit") {
		t.Fatal("Docker without GPU support not explained")
	}
	other := errors.New("port already allocated")
	if noGpuSupport(other) || gpuStartError(other) != other {
		t.Fatal("another error was taken for a GPU one")
	}
}
