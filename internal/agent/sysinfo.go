package agent

import (
	"context"
	"os"
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/docker"
)

// diskPath is the filesystem whose size is reported. In a container, the host's root is mounted read-only
// and named by FORGEYARD_HOST_ROOT, so the host's disk is measured rather than the container's.
func diskPath() string {
	if root := os.Getenv("FORGEYARD_HOST_ROOT"); root != "" {
		return root
	}
	return "/"
}

func nodeInfo(ctx context.Context, dc *docker.Client) *agentpb.NodeInfo {
	info := &agentpb.NodeInfo{Os: runtime.GOOS, Arch: runtime.GOARCH}
	if h, err := host.InfoWithContext(ctx); err == nil {
		info.Hostname = h.Hostname
		info.Os = h.Platform + " " + h.PlatformVersion
	}
	if n, err := cpu.CountsWithContext(ctx, true); err == nil {
		info.Cpus = int32(n)
	}
	if v, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		info.MemoryBytes = v.Total
	}
	if d, err := disk.UsageWithContext(ctx, diskPath()); err == nil {
		info.DiskBytes = d.Total
	}
	// Docker describes the host itself, which gopsutil cannot do from inside a container.
	if dc != nil {
		if di, err := dc.Info(ctx); err == nil {
			info.DockerVersion = di.ServerVersion
			if di.Name != "" {
				info.Hostname = di.Name
			}
			if di.OperatingSystem != "" {
				info.Os = di.OperatingSystem
			}
			if di.NCPU > 0 {
				info.Cpus = int32(di.NCPU)
			}
			if di.MemTotal > 0 {
				info.MemoryBytes = di.MemTotal
			}
		}
	}
	return info
}

func sampleMetrics(ctx context.Context, dc *docker.Client) *agentpb.Metrics {
	m := &agentpb.Metrics{UnixTime: time.Now().Unix()}
	// Interval 0 measures since the previous call, i.e. over the last sampling period.
	if p, err := cpu.PercentWithContext(ctx, 0, false); err == nil && len(p) == 1 {
		m.CpuPercent = p[0]
	}
	if v, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		m.MemoryUsedBytes = v.Used
	}
	if d, err := disk.UsageWithContext(ctx, diskPath()); err == nil {
		m.DiskUsedBytes = d.Used
	}
	if dc != nil {
		if di, err := dc.Info(ctx); err == nil {
			m.ContainersRunning = int32(di.ContainersRunning)
		}
	}
	return m
}
