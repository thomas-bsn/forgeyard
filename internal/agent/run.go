package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/docker"
)

// Version is reported to the control plane.
const Version = "0.1.0-dev"

const (
	metricsInterval = 5 * time.Second
	maxBackoff      = 30 * time.Second
)

// ErrRemoved means the control plane no longer accepts this node; retrying is pointless.
var ErrRemoved = errors.New("this node was removed from the server: join again with a new command from the Nodes page")

// Run keeps a stream open to the control plane until ctx ends, reconnecting with backoff.
func Run(ctx context.Context, st *State, dc *docker.Client, logger *slog.Logger) error {
	conn, err := grpc.NewClient(st.AgentServer,
		grpc.WithTransportCredentials(credentials.NewTLS(st.TLS)),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 30 * time.Second, Timeout: 10 * time.Second, PermitWithoutStream: true}),
	)
	if err != nil {
		return err
	}
	defer conn.Close()
	client := agentpb.NewAgentServiceClient(conn)

	backoff := time.Second
	for {
		connected, err := session(ctx, client, dc, logger)
		if ctx.Err() != nil {
			return nil
		}
		if status.Code(err) == codes.PermissionDenied {
			return fmt.Errorf("%w (%v)", ErrRemoved, status.Convert(err).Message())
		}
		if connected {
			backoff = time.Second
		}
		logger.Warn("connection to the server lost, retrying", "server", st.AgentServer, "in", backoff, "err", err)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// session runs one stream. connected reports whether the server accepted it, to reset the backoff.
func session(ctx context.Context, client agentpb.AgentServiceClient, dc *docker.Client, logger *slog.Logger) (connected bool, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := client.Connect(ctx)
	if err != nil {
		return false, err
	}
	// A failed Send only says io.EOF; the reason (e.g. PermissionDenied for a removed node) comes from
	// Recv, so the error is always read there.
	_ = stream.Send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_Hello{
		Hello: &agentpb.Hello{AgentVersion: Version, Info: nodeInfo(ctx, dc)},
	}})
	msg, err := stream.Recv()
	if err != nil {
		return false, err
	}
	welcome := msg.GetWelcome()
	if welcome == nil {
		return false, errors.New("unexpected first message from the server")
	}
	logger.Info("connected to the server", "node", welcome.GetNodeName())

	go func() {
		ticker := time.NewTicker(metricsInterval)
		defer ticker.Stop()
		for {
			if err := stream.Send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_Metrics{Metrics: sampleMetrics(ctx, dc)}}); err != nil {
				return // the stream is broken: Recv below reports why
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	recvErr := make(chan error, 1)
	go func() {
		for {
			if _, err := stream.Recv(); err != nil {
				recvErr <- err
				return
			}
			// No server-to-agent commands yet: containers come in the next step.
		}
	}()

	select {
	case <-ctx.Done():
		return true, nil
	case err := <-recvErr:
		return true, err
	}
}
