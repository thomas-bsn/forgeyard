package agent

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/docker"
)

// While an app of the node uses GPUs, a small container runs nvidia-smi in a loop (one line per GPU every
// gpuMonitorEvery) and the agent reads its output: one container for every GPU, instead of one per
// measure. The figures are those of each GPU as a whole: Docker does not measure GPUs per container.

const (
	gpuMonitorName  = "forgeyard-gpu-monitor"
	gpuMonitorEvery = 5 // seconds
	gpuMetricStale  = 30 * time.Second
)

type gpuReading struct {
	m  *agentpb.GpuMetric
	at time.Time
}

type gpuMonitor struct {
	dc     *docker.Client
	logger *slog.Logger

	mu      sync.Mutex
	latest  map[string]gpuReading // by GPU id
	cancel  context.CancelFunc    // stops following the container's output
	running bool
	retryAt time.Time // after a failed start, not before
}

func newGpuMonitor(dc *docker.Client, logger *slog.Logger) *gpuMonitor {
	return &gpuMonitor{dc: dc, logger: logger, latest: map[string]gpuReading{}}
}

// sync runs the monitor while wanted, and removes it otherwise.
func (g *gpuMonitor) sync(ctx context.Context, wanted bool) {
	g.mu.Lock()
	running := g.running
	g.mu.Unlock()
	if !wanted {
		if running {
			g.stop(ctx)
		}
		return
	}
	if running || time.Now().Before(g.retryAt) {
		return
	}
	if err := g.start(ctx); err != nil {
		g.retryAt = time.Now().Add(time.Minute)
		g.logger.Warn("starting the GPU monitor failed", "err", err)
	}
}

func (g *gpuMonitor) start(ctx context.Context) error {
	ct, err := g.dc.Inspect(ctx, gpuMonitorName)
	if err != nil && !errors.Is(err, docker.ErrNotFound) {
		return err
	}
	if err != nil || !ct.State.Running {
		g.dc.Remove(ctx, gpuMonitorName)
		if err := g.dc.Pull(ctx, gpuProbeImage); err != nil {
			return err
		}
		if err := g.dc.Create(ctx, gpuMonitorName, map[string]any{
			"Image": gpuProbeImage,
			"Cmd": []string{"nvidia-smi", "--query-gpu=uuid,utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw",
				"--format=csv,noheader,nounits", "-l", strconv.Itoa(gpuMonitorEvery)},
			"Labels": map[string]string{labelInternal: "gpu-monitor"},
			"HostConfig": map[string]any{
				"DeviceRequests": []map[string]any{{"Driver": vendorNvidia, "Count": -1, "Capabilities": [][]string{{"gpu", "utility"}}}},
				"RestartPolicy":  map[string]any{"Name": "unless-stopped"},
				"Memory":         64 << 20,
				"LogConfig":      map[string]any{"Type": "json-file", "Config": map[string]string{"max-size": "1m", "max-file": "1"}},
			},
		}); err != nil {
			return err
		}
		if err := g.dc.Start(ctx, gpuMonitorName); err != nil {
			g.dc.Remove(ctx, gpuMonitorName)
			return err
		}
	}
	fctx, cancel := context.WithCancel(ctx)
	g.mu.Lock()
	g.cancel, g.running = cancel, true
	g.mu.Unlock()
	go func() {
		g.dc.Logs(fctx, gpuMonitorName, 0, func(l docker.LogLine) {
			if m := parseGpuMetric(l.Text); m != nil {
				g.mu.Lock()
				g.latest[m.GetId()] = gpuReading{m: m, at: time.Now()}
				g.mu.Unlock()
			}
		})
		// The output ended (container gone, connection lost): start again at the next sync.
		g.mu.Lock()
		g.running = false
		g.mu.Unlock()
	}()
	return nil
}

func (g *gpuMonitor) stop(ctx context.Context) {
	g.mu.Lock()
	if g.cancel != nil {
		g.cancel()
	}
	g.running, g.latest = false, map[string]gpuReading{}
	g.mu.Unlock()
	if err := g.dc.Remove(ctx, gpuMonitorName); err != nil {
		g.logger.Warn("removing the GPU monitor failed", "err", err)
	}
}

// metrics are the last readings of each GPU, the stale ones left out.
func (g *gpuMonitor) metrics() []*agentpb.GpuMetric {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []*agentpb.GpuMetric
	for _, r := range g.latest {
		if time.Since(r.at) < gpuMetricStale {
			out = append(out, r.m)
		}
	}
	return out
}

// parseGpuMetric reads a line of `nvidia-smi --query-gpu=uuid,utilization.gpu,memory.used,memory.total,
// temperature.gpu,power.draw --format=csv,noheader,nounits` (a log line starts with Docker's timestamp).
// A figure the GPU does not give ("[N/A]") is left at 0.
func parseGpuMetric(line string) *agentpb.GpuMetric {
	if _, rest, ok := strings.Cut(line, " "); ok && strings.HasPrefix(line, "20") && strings.Contains(line[:min(len(line), 40)], "T") {
		line = rest
	}
	f := strings.Split(line, ",")
	if len(f) < 6 {
		return nil
	}
	num := func(s string) float64 {
		v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
		return v
	}
	id := strings.TrimSpace(f[0])
	if !strings.HasPrefix(id, "GPU-") {
		return nil
	}
	return &agentpb.GpuMetric{
		Id: id, UtilizationPercent: num(f[1]), MemoryUsedBytes: int64(num(f[2])) << 20, MemoryTotalBytes: int64(num(f[3])) << 20,
		TemperatureC: num(f[4]), PowerWatts: num(f[5]),
	}
}
