package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/docker"
)

// labelInternal marks Forgeyard's own containers (server, agent), which are never shown nor controlled as
// external containers.
const labelInternal = "forgeyard.internal"

// externals watches the node's containers that Forgeyard did not create.
type externals struct {
	dc *docker.Client

	mu      sync.Mutex
	cpuSeen map[string]cpuSample // by container ID
}

func newExternals(dc *docker.Client) *externals {
	return &externals{dc: dc, cpuSeen: map[string]cpuSample{}}
}

// isExternal reports whether a container is neither an app nor part of Forgeyard. The agent's own
// container is recognized by its hostname, which Docker sets to the short container ID.
func isExternal(c docker.Summary) bool {
	for _, l := range []string{labelApp, labelIngress, labelInternal} {
		if _, ok := c.Labels[l]; ok {
			return false
		}
	}
	if host, err := os.Hostname(); err == nil && len(host) >= 12 && strings.HasPrefix(c.ID, host) {
		return false
	}
	return true
}

// list describes the external containers, with the resource use of the running ones.
func (e *externals) list(ctx context.Context) ([]*agentpb.ExternalContainer, error) {
	all, err := e.dc.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	out := []*agentpb.ExternalContainer{}
	seen := map[string]bool{}
	for _, c := range all {
		if !isExternal(c) {
			continue
		}
		ec := &agentpb.ExternalContainer{
			Id: c.ID, Name: c.Name(), Image: c.Image, State: c.State, Status: c.Status, CreatedAt: c.Created,
			ComposeProject: c.Labels["com.docker.compose.project"],
		}
		for _, p := range c.Ports {
			if p.PublicPort > 0 {
				port := fmt.Sprintf("%d->%d/%s", p.PublicPort, p.PrivatePort, p.Type)
				if !slices.Contains(ec.Ports, port) { // listed once per IPv4 and IPv6
					ec.Ports = append(ec.Ports, port)
				}
			}
		}
		if c.State == "running" {
			seen[c.ID] = true
			e.sampleUsage(ctx, ec)
		}
		out = append(out, ec)
	}
	e.mu.Lock()
	for id := range e.cpuSeen {
		if !seen[id] {
			delete(e.cpuSeen, id)
		}
	}
	e.mu.Unlock()
	return out, nil
}

func (e *externals) sampleUsage(ctx context.Context, ec *agentpb.ExternalContainer) {
	stats, err := e.dc.Stats(ctx, ec.Id)
	if err != nil {
		return
	}
	ec.MemoryUsedBytes = stats.MemoryUsed()
	cur := cpuSample{total: stats.CPUStats.CPUUsage.TotalUsage, system: stats.CPUStats.SystemUsage}
	e.mu.Lock()
	prev, ok := e.cpuSeen[ec.Id]
	e.cpuSeen[ec.Id] = cur
	e.mu.Unlock()
	if ok && cur.total >= prev.total && cur.system > prev.system {
		cpus := max(stats.CPUStats.OnlineCPUs, 1)
		ec.CpuPercent = float64(cur.total-prev.total) / float64(cur.system-prev.system) * float64(cpus) * 100
	}
}

// external returns the external container with this ID, refusing apps and Forgeyard's own containers.
func (e *externals) external(ctx context.Context, id string) (docker.Summary, error) {
	all, err := e.dc.ListAll(ctx)
	if err != nil {
		return docker.Summary{}, err
	}
	for _, c := range all {
		if c.ID == id && isExternal(c) {
			return c, nil
		}
	}
	return docker.Summary{}, errors.New("not an external container of this node")
}

// act starts, stops or restarts an external container.
func (e *externals) act(ctx context.Context, a *agentpb.ContainerAction) error {
	c, err := e.external(ctx, a.GetContainerId())
	if err != nil {
		return err
	}
	switch a.GetAction() {
	case "start":
		return e.dc.Start(ctx, c.ID)
	case "stop":
		return e.dc.Stop(ctx, c.ID)
	case "restart":
		return e.dc.Restart(ctx, c.ID)
	}
	return fmt.Errorf("unknown action %q", a.GetAction())
}
