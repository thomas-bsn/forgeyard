package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/docker"
)

// helperName is the short-lived container that reads the host's IP from the host's network.
const helperName = "forgeyard-host-ip"

// RouteIP is the source address of the default route: the machine's IP on its local network. Dialing UDP
// sends nothing; it only makes the kernel choose the route.
func RouteIP() string {
	conn, err := net.Dial("udp", "1.1.1.1:53")
	if err != nil {
		return ""
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || addr.IP.IsLoopback() {
		return ""
	}
	return addr.IP.String()
}

// LocalIPEnv sets the host's local IP by hand, for the machines where it cannot be found: with Docker
// Desktop (WSL2 on Windows, macOS), the host's network seen from a container is Docker's VM, whose address
// (192.168.65.3) the other machines cannot reach.
const LocalIPEnv = "FORGEYARD_LOCAL_IP"

// ParseLocalIP checks an address given by hand: an IP the other nodes can dial, so neither loopback,
// unspecified, link-local nor multicast.
func ParseLocalIP(s string) (string, error) {
	addr, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil || addr.Zone() != "" {
		return "", fmt.Errorf("%s=%q : pas une adresse IP, par exemple 192.168.1.17", LocalIPEnv, s)
	}
	addr = addr.Unmap()
	if addr.IsUnspecified() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsMulticast() {
		return "", fmt.Errorf("%s=%s : les autres machines ne peuvent pas joindre cette adresse, indiquez l'IP de la machine sur le réseau local", LocalIPEnv, addr)
	}
	return addr.String(), nil
}

// LocalIPOverride returns the address set with LocalIPEnv, "" when none is.
func LocalIPOverride() (string, error) {
	v, ok := os.LookupEnv(LocalIPEnv)
	if !ok || strings.TrimSpace(v) == "" {
		return "", nil
	}
	return ParseLocalIP(v)
}

var (
	localIPOnce sync.Once
	localIP     string
)

// LocalIP returns the host's IP on its local network, which a reverse proxy, and Forgeyard's machine for
// the nodes it relays, use to reach Traefik. LocalIPEnv wins; otherwise, in a container, the agent only sees
// its own network, so it runs its own image once on the host's network to read it. The result is kept for
// the agent's lifetime: a new address is taken by restarting the agent, whose next Hello updates the relays.
func LocalIP(ctx context.Context, dc *docker.Client, logger *slog.Logger) string {
	localIPOnce.Do(func() {
		_, err := os.Stat("/.dockerenv")
		src := localIPSources{inContainer: err == nil, routeIP: RouteIP}
		src.override, src.overrideErr = LocalIPOverride()
		if dc != nil {
			src.dockerOS = func() (string, error) {
				info, err := dc.Info(ctx)
				return info.OperatingSystem, err
			}
			src.helperIP = func() (string, error) { return hostIPFromHelper(ctx, dc) }
		}
		localIP = resolveLocalIP(src, logger)
	})
	return localIP
}

// localIPSources are where the local IP may come from, apart so that the choice can be tested.
type localIPSources struct {
	override    string
	overrideErr error
	inContainer bool
	routeIP     func() string
	dockerOS    func() (string, error) // the OperatingSystem Docker reports; nil without Docker
	helperIP    func() (string, error) // nil without Docker
}

// resolveLocalIP picks the local IP. No IP is better than a wrong one: without it, the server takes the
// address the agent connects from when it is a local one, and otherwise says the address is missing,
// instead of relaying to an address nobody reaches.
func resolveLocalIP(src localIPSources, logger *slog.Logger) string {
	if src.overrideErr != nil {
		// Not a fallback to the detection: it is what the address was set by hand to avoid.
		logger.Error("ignoring the local IP set by hand", "err", src.overrideErr)
		return ""
	}
	if src.override != "" {
		logger.Info("local IP set by hand", "env", LocalIPEnv, "ip", src.override)
		return src.override
	}
	if !src.inContainer {
		return src.routeIP()
	}
	if src.helperIP == nil {
		return ""
	}
	if src.dockerOS != nil {
		if name, err := src.dockerOS(); err == nil && name == dockerDesktopOS {
			logger.Warn("Docker Desktop: the host's network is Docker's VM, whose IP the other machines cannot reach; set the machine's IP on the local network with "+LocalIPEnv, "env", LocalIPEnv)
			return ""
		}
	}
	ip, err := src.helperIP()
	if err != nil {
		logger.Warn("finding the host's local IP failed", "err", err)
		return ""
	}
	return ip
}

// dockerDesktopOS is the OperatingSystem Docker Desktop reports, on Windows (WSL2 or Hyper-V) and macOS.
const dockerDesktopOS = "Docker Desktop"

func hostIPFromHelper(ctx context.Context, dc *docker.Client) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	host, err := os.Hostname()
	if err != nil {
		return "", err
	}
	self, err := dc.Inspect(ctx, host) // Docker sets the hostname to the short container ID
	if err != nil {
		return "", err
	}
	dc.Remove(ctx, helperName) // a leftover from an interrupted run
	defer dc.Remove(context.Background(), helperName)
	if err := dc.Create(ctx, helperName, map[string]any{
		"Image":      self.Config.Image,
		"Entrypoint": []string{"forgeyard-agent"},
		"Cmd":        []string{"host-ip"},
		"Labels":     map[string]string{labelInternal: "helper"},
		"HostConfig": map[string]any{"NetworkMode": "host"},
	}); err != nil {
		return "", err
	}
	if err := dc.Start(ctx, helperName); err != nil {
		return "", err
	}
	if err := dc.Wait(ctx, helperName); err != nil {
		return "", err
	}
	out, err := dc.Output(ctx, helperName)
	if err != nil {
		return "", err
	}
	ip := net.ParseIP(strings.TrimSpace(out))
	if ip == nil {
		return "", errors.New("unexpected output: " + strings.TrimSpace(out))
	}
	return ip.String(), nil
}
