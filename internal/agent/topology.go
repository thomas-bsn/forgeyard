package agent

import (
	"context"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/docker"
)

// topology describes how the node's containers are wired, for the topology views: the Docker networks,
// each container's addresses and names on them, the ports it publishes on the node and the ones it
// really listens on. Being on the same network only means two containers can talk, not that they do.
func topology(ctx context.Context, dc *docker.Client) (*agentpb.Topology, error) {
	nets, err := dc.ListNetworks(ctx)
	if err != nil {
		return nil, err
	}
	all, err := dc.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	t := &agentpb.Topology{}
	for _, n := range nets {
		dn := &agentpb.DockerNetwork{Id: n.ID, Name: n.Name, Driver: n.Driver, Internal: n.Internal}
		if len(n.IPAM.Config) > 0 {
			dn.Subnet = n.IPAM.Config[0].Subnet
		}
		t.Networks = append(t.Networks, dn)
	}
	self, _ := os.Hostname()
	for _, c := range all {
		role := containerRole(c, self)
		if role == "helper" {
			continue // short-lived: the host IP probe, the updater
		}
		tc := &agentpb.TopoContainer{
			Id: c.ID, Name: c.Name(), Image: c.Image, State: c.State, Role: role,
			ComposeProject: c.Labels["com.docker.compose.project"],
		}
		if role == "app" {
			tc.AppId, _ = strconv.ParseInt(c.Labels[labelApp], 10, 64)
		}
		// A running container's inspection has its aliases, which the list leaves out.
		networks := c.NetworkSettings.Networks
		if c.State == "running" {
			if ct, err := dc.Inspect(ctx, c.ID); err == nil {
				tc.Listening = listeningPorts(ct.State.Pid)
				if len(ct.NetworkSettings.Networks) > 0 {
					networks = ct.NetworkSettings.Networks
				}
			}
		}
		for name, ep := range networks {
			aliases := ep.DNSNames
			if len(aliases) == 0 {
				aliases = ep.Aliases
			}
			var keep []string
			for _, a := range aliases {
				// Docker adds the short container ID: true but of no help to a reader.
				if !strings.HasPrefix(c.ID, a) && !slices.Contains(keep, a) {
					keep = append(keep, a)
				}
			}
			tc.Endpoints = append(tc.Endpoints, &agentpb.NetworkEndpoint{Network: name, Ipv4: ep.IPAddress, Aliases: keep})
		}
		slices.SortFunc(tc.Endpoints, func(a, b *agentpb.NetworkEndpoint) int { return strings.Compare(a.Network, b.Network) })
		for _, p := range c.Ports {
			if p.PublicPort == 0 {
				continue
			}
			dup := slices.ContainsFunc(tc.Published, func(x *agentpb.PublishedPort) bool {
				return x.ContainerPort == int32(p.PrivatePort) && x.HostPort == int32(p.PublicPort) && x.Protocol == p.Type
			})
			if !dup { // listed once per IPv4 and IPv6
				tc.Published = append(tc.Published, &agentpb.PublishedPort{ContainerPort: int32(p.PrivatePort), HostPort: int32(p.PublicPort), Protocol: p.Type, HostIp: p.IP})
			}
		}
		t.Containers = append(t.Containers, tc)
	}
	return t, nil
}

// containerRole tells what a container is to Forgeyard.
func containerRole(c docker.Summary, self string) string {
	switch {
	case c.Labels[labelApp] != "":
		return "app"
	case c.Labels[labelIngress] != "":
		return "traefik"
	case c.Labels[labelInternal] != "":
		return c.Labels[labelInternal] // server, agent or helper
	case self != "" && len(self) >= 12 && strings.HasPrefix(c.ID, self):
		return "agent"
	}
	return "external"
}
