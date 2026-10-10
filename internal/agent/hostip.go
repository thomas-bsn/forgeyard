package agent

import (
	"context"
	"errors"
	"log/slog"
	"net"
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

var (
	localIPOnce sync.Once
	localIP     string
)

// LocalIP returns the host's IP on its local network, which a reverse proxy uses to reach Traefik. In a
// container, the agent only sees its own network, so it runs its own image once on the host's network to
// read it. The result is kept for the agent's lifetime.
func LocalIP(ctx context.Context, dc *docker.Client, logger *slog.Logger) string {
	localIPOnce.Do(func() {
		if _, err := os.Stat("/.dockerenv"); err != nil {
			localIP = RouteIP()
			return
		}
		if dc == nil {
			return
		}
		ip, err := hostIPFromHelper(ctx, dc)
		if err != nil {
			logger.Warn("finding the host's local IP failed", "err", err)
		}
		localIP = ip
	})
	return localIP
}

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
