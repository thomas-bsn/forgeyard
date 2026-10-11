package agent

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/docker"
	"github.com/thomas-bsn/forgeyard/internal/tunnel"
)

// openTunnel answers TunnelOpen: it connects a Tunnel stream to the node's Traefik, and carries the bytes
// both ways until either side closes.
func openTunnel(ctx context.Context, client agentpb.AgentServiceClient, dc *docker.Client, id string, logger *slog.Logger) {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := client.Tunnel(sctx)
	if err != nil {
		logger.Warn("opening a tunnel failed", "err", err)
		return
	}
	if err := stream.Send(&agentpb.TunnelChunk{TunnelId: id}); err != nil {
		return
	}
	traefik, err := dialTraefik(ctx, dc)
	if err != nil {
		// Closing the stream tells the server at once, instead of letting it wait.
		logger.Warn("reaching Traefik for a tunnel failed", "err", err)
		return
	}
	// Closing sends the end of the stream after the data written so far; the stream itself ends once the
	// server is done too.
	conn := tunnel.New(stream, nil, func() { stream.CloseSend() })
	tunnel.Pipe(conn, traefik)
}

// dialTraefik connects to the node's Traefik on the forgeyard network, which the agent joins when it runs
// in a container (outside one, the host reaches the network's addresses).
func dialTraefik(ctx context.Context, dc *docker.Client) (net.Conn, error) {
	ct, err := dc.Inspect(ctx, traefikName)
	if err != nil {
		return nil, err
	}
	ip := ct.NetworkSettings.Networks[networkName].IPAddress
	if !ct.State.Running || ip == "" {
		return nil, errors.New("Traefik is not running")
	}
	if self, err := selfContainer(ctx, dc); err == nil {
		if _, joined := self.NetworkSettings.Networks[networkName]; !joined {
			if err := dc.ConnectNetwork(ctx, networkName, self.ID); err != nil {
				return nil, err
			}
		}
	}
	d := net.Dialer{Timeout: 5 * time.Second}
	return d.DialContext(ctx, "tcp", net.JoinHostPort(ip, "80"))
}
