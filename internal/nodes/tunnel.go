package nodes

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/tunnel"
)

// A node in ingress mode "tunnel" receives its visits through its agent: for each connection, the control
// plane asks the agent (on its Connect stream) to open a Tunnel stream, which the agent connects to its
// node's Traefik. The node needs no inbound port, only its usual outbound connection.

// tunnelOpenTimeout bounds the wait for the agent to open the stream it was asked for.
const tunnelOpenTimeout = 10 * time.Second

type tunnelWaiter struct {
	nodeID int64
	conn   chan *tunnel.Conn
	failed chan string // the agent could not connect the stream, and says why
}

// DialTunnel returns a connection to a node's Traefik, through its agent.
func (h *Hub) DialTunnel(ctx context.Context, nodeID int64) (net.Conn, error) {
	return h.dialTunnel(ctx, nodeID, &agentpb.TunnelOpen{}, tunnelOpenTimeout)
}

// DialSFTP returns a connection to an SFTP server in an app's container, through its node's agent.
func (h *Hub) DialSFTP(ctx context.Context, nodeID, appID int64) (net.Conn, error) {
	// The agent may first copy the SFTP server into the container.
	return h.dialTunnel(ctx, nodeID, &agentpb.TunnelOpen{SftpAppId: appID}, time.Minute)
}

func (h *Hub) dialTunnel(ctx context.Context, nodeID int64, open *agentpb.TunnelOpen, timeout time.Duration) (net.Conn, error) {
	id := randomID()
	open.TunnelId = id
	w := &tunnelWaiter{nodeID: nodeID, conn: make(chan *tunnel.Conn, 1), failed: make(chan string, 1)}
	h.mu.Lock()
	h.tunnels[id] = w
	h.mu.Unlock()
	drop := func() {
		h.mu.Lock()
		delete(h.tunnels, id)
		h.mu.Unlock()
		// The agent may have opened it just now.
		select {
		case c := <-w.conn:
			c.Close()
		default:
		}
	}
	if !h.send(nodeID, &agentpb.ServerMessage{Msg: &agentpb.ServerMessage_TunnelOpen{TunnelOpen: open}}) {
		drop()
		return nil, ErrOffline
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case c := <-w.conn:
		return c, nil
	case msg := <-w.failed:
		return nil, errors.New(msg)
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	case <-timer.C:
		drop()
		return nil, errors.New("the node's agent did not open the tunnel")
	}
}

// Tunnel serves a stream an agent opened after TunnelOpen, until either side closes it.
func (h *Hub) Tunnel(stream agentpb.AgentService_TunnelServer) error {
	node, err := h.authenticate(stream.Context())
	if err != nil {
		return err
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	if rest, ok := strings.CutPrefix(first.GetTunnelId(), "source/"); ok {
		return h.serveSource(stream, node.ID, rest)
	}
	h.mu.Lock()
	w, ok := h.tunnels[first.GetTunnelId()]
	if ok && w.nodeID == node.ID {
		delete(h.tunnels, first.GetTunnelId())
	}
	h.mu.Unlock()
	if !ok {
		return status.Error(codes.NotFound, "no such tunnel, or it was given up")
	}
	if w.nodeID != node.ID {
		return status.Error(codes.PermissionDenied, "this tunnel is another node's")
	}
	if first.GetEof() {
		w.failed <- string(first.GetData())
		return nil
	}
	done := make(chan struct{})
	w.conn <- tunnel.New(stream, first.GetData(), func() { close(done) })
	select {
	case <-done:
	case <-stream.Context().Done():
	}
	return nil
}

// SourceFunc serves the files of an app's git repository at a commit to a node building it: it checks
// that the node runs the app at that commit, sends the files as a tar archive, then a chunk with eof set,
// and reads the build's output until the agent's chunk with eof set, whose data is the build's error.
type SourceFunc func(ctx context.Context, nodeID, appID int64, commit string, stream agentpb.AgentService_TunnelServer) error

// serveSource answers a stream an agent opened to build an app from its repository.
func (h *Hub) serveSource(stream agentpb.AgentService_TunnelServer, nodeID int64, name string) error {
	app, commit, ok := strings.Cut(name, "/")
	appID, err := strconv.ParseInt(app, 10, 64)
	if !ok || err != nil || h.Source == nil {
		return status.Error(codes.InvalidArgument, "unknown source")
	}
	return h.Source(stream.Context(), nodeID, appID, commit, stream)
}
