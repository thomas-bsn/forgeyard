package agent

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/docker"
)

// GPUs: a GPU in the machine is not enough, Docker must be able to give it to a container (an NVIDIA driver
// and the NVIDIA Container Toolkit, built into Docker Desktop with WSL2). So the agent checks the way an app
// will use it: it starts a short-lived container asking Docker for every NVIDIA GPU, which lists them with
// nvidia-smi (the toolkit puts it in the container). Only NVIDIA for now: the vendor travels with the
// requests, for others later.

const (
	vendorNvidia  = "nvidia"
	gpuProbeName  = "forgeyard-gpu-probe"
	gpuProbeImage = "debian:bookworm-slim" // nvidia-smi needs glibc
)

// gpuDeviceRequests is Docker's form of an app's GPU request, as `docker run --gpus` sends it.
func gpuDeviceRequests(g *agentpb.GpuRequest) []map[string]any {
	req := map[string]any{"Driver": g.GetVendor(), "Capabilities": [][]string{{"gpu"}}}
	if g.GetAll() {
		req["Count"] = -1
	} else {
		req["DeviceIDs"] = g.GetDeviceIds()
	}
	return []map[string]any{req}
}

// noGpuSupport tells whether Docker refused a GPU request because it cannot give GPUs at all.
func noGpuSupport(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, s := range []string{"could not select device driver", "nvidia-container-cli", "unknown or invalid runtime name: nvidia"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// gpuStartError explains a container that could not start because of its GPU request.
func gpuStartError(err error) error {
	if !noGpuSupport(err) {
		return err
	}
	return fmt.Errorf("Docker de ce node ne peut pas donner de GPU aux conteneurs (pilote NVIDIA ou NVIDIA Container Toolkit absent) : %w", err)
}

// probeGpus checks what GPUs the node's Docker gives to containers.
func probeGpus(ctx context.Context, dc *docker.Client) *agentpb.NodeGpus {
	if err := dc.Pull(ctx, gpuProbeImage); err != nil {
		return &agentpb.NodeGpus{Status: "error", Error: "téléchargement de " + gpuProbeImage + " : " + err.Error()}
	}
	res, err := dc.Run(ctx, gpuProbeName, map[string]any{
		"Image":  gpuProbeImage,
		"Cmd":    []string{"nvidia-smi", "--query-gpu=index,uuid,name,memory.total", "--format=csv,noheader,nounits"},
		"Labels": map[string]string{labelInternal: "gpu-probe"},
		"HostConfig": map[string]any{
			"DeviceRequests": []map[string]any{{"Driver": vendorNvidia, "Count": -1, "Capabilities": [][]string{{"gpu", "utility"}}}},
			"LogConfig":      map[string]any{"Type": "json-file"},
		},
	}, nil)
	switch {
	case noGpuSupport(err):
		return &agentpb.NodeGpus{Status: "unsupported", Error: "Docker ne peut pas donner de GPU NVIDIA aux conteneurs : pas de pilote NVIDIA, ou pas de NVIDIA Container Toolkit (" + lastLine(err.Error()) + ")"}
	case err != nil:
		return &agentpb.NodeGpus{Status: "error", Error: err.Error()}
	case res.ExitCode != 0:
		// Docker gave the GPUs, but nvidia-smi could not list them: they can still be used, all of them.
		return &agentpb.NodeGpus{Status: "ok", Error: fmt.Sprintf("nvidia-smi a échoué (code %d) : %s", res.ExitCode, lastLine(res.Stderr+res.Stdout))}
	}
	return &agentpb.NodeGpus{Status: "ok", Gpus: parseNvidiaSmi(res.Stdout)}
}

// parseNvidiaSmi reads `nvidia-smi --query-gpu=index,uuid,name,memory.total --format=csv,noheader,nounits`:
// one line per GPU, its memory in MiB.
func parseNvidiaSmi(out string) []*agentpb.Gpu {
	var gpus []*agentpb.Gpu
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, ",")
		if len(f) < 4 {
			continue
		}
		for i := range f {
			f[i] = strings.TrimSpace(f[i])
		}
		index, err := strconv.Atoi(f[0])
		if err != nil || f[1] == "" {
			continue
		}
		mib, _ := strconv.ParseInt(f[3], 10, 64)
		gpus = append(gpus, &agentpb.Gpu{Vendor: vendorNvidia, Index: int32(index), Id: f[1], Name: f[2], MemoryBytes: mib << 20})
	}
	return gpus
}

// gpuState keeps the last check, sent again on each connection, and runs one check at a time.
type gpuState struct {
	mu      sync.Mutex
	last    *agentpb.NodeGpus
	running bool
}

func (g *gpuState) get() *agentpb.NodeGpus {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.last
}

// check probes the GPUs and hands the result to send; a check already running is not doubled.
func (g *gpuState) check(ctx context.Context, dc *docker.Client, send func(*agentpb.NodeGpus)) {
	g.mu.Lock()
	if g.running {
		g.mu.Unlock()
		return
	}
	g.running = true
	g.mu.Unlock()
	res := probeGpus(ctx, dc)
	if errors.Is(ctx.Err(), context.Canceled) {
		g.mu.Lock()
		g.running = false
		g.mu.Unlock()
		return
	}
	g.mu.Lock()
	g.last, g.running = res, false
	g.mu.Unlock()
	send(res)
}
