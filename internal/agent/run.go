package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
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
const Version = "0.2.0-dev"

const (
	metricsInterval = 5 * time.Second
	statusInterval  = 3 * time.Second
	maxBackoff      = 30 * time.Second
)

// ErrRemoved means the control plane no longer accepts this node; retrying is pointless.
var ErrRemoved = errors.New("this node was removed from the server: join again with a new command from the Nodes page")

// Run keeps a stream open to the control plane until ctx ends, reconnecting with backoff. Containers keep
// running while the stream is down, and the last desired state keeps being enforced.
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

	var rec *Reconciler
	if dc != nil {
		rec = NewReconciler(dc, logger)
		go rec.Run(ctx)
	}

	backoff := time.Second
	for {
		connected, err := session(ctx, client, dc, rec, logger)
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
func session(ctx context.Context, client agentpb.AgentServiceClient, dc *docker.Client, rec *Reconciler, logger *slog.Logger) (connected bool, err error) {
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

	// gRPC streams allow one sender at a time: everything goes through out.
	out := make(chan *agentpb.AgentMessage, 64)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case m := <-out:
				if err := stream.Send(m); err != nil {
					return // the stream is broken: Recv below reports why
				}
			}
		}
	}()
	send := func(m *agentpb.AgentMessage) {
		select {
		case out <- m:
		case <-ctx.Done():
		}
	}

	go func() {
		metrics := time.NewTicker(metricsInterval)
		statuses := time.NewTicker(statusInterval)
		defer metrics.Stop()
		defer statuses.Stop()
		send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_Metrics{Metrics: sampleMetrics(ctx, dc)}})
		for {
			select {
			case <-ctx.Done():
				return
			case <-metrics.C:
				send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_Metrics{Metrics: sampleMetrics(ctx, dc)}})
			case <-statuses.C:
				if rec != nil {
					send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_AppStatuses{
						AppStatuses: &agentpb.AppStatuses{Apps: rec.Statuses(ctx)},
					}})
				}
			}
		}
	}()

	logs := newLogStreams()
	defer logs.stopAll()
	for {
		msg, err := stream.Recv()
		if err != nil {
			return true, err
		}
		switch m := msg.GetMsg().(type) {
		case *agentpb.ServerMessage_DesiredState:
			if rec != nil {
				rec.SetDesired(m.DesiredState)
			}
		case *agentpb.ServerMessage_StartLogs:
			if dc != nil {
				logs.start(ctx, dc, m.StartLogs, send)
			}
		case *agentpb.ServerMessage_StopLogs:
			logs.stop(m.StopLogs.GetStreamId())
		}
	}
}

// logStreams are the container log streams the control plane is watching.
type logStreams struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func newLogStreams() *logStreams {
	return &logStreams{cancels: map[string]context.CancelFunc{}}
}

func (l *logStreams) start(ctx context.Context, dc *docker.Client, req *agentpb.StartLogs, send func(*agentpb.AgentMessage)) {
	ctx, cancel := context.WithCancel(ctx)
	id := req.GetStreamId()
	l.mu.Lock()
	l.cancels[id] = cancel
	l.mu.Unlock()
	go func() {
		defer l.stop(id)
		line := func(text string, stderr, end bool) {
			send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_LogLine{LogLine: &agentpb.LogLine{
				StreamId: id, Text: text, Stderr: stderr, End: end,
			}}})
		}
		err := dc.Logs(ctx, containerName(req.GetAppId()), int(req.GetTail()), func(l docker.LogLine) {
			line(l.Text, l.Stderr, false)
		})
		if ctx.Err() != nil {
			return // stopped by the control plane
		}
		switch {
		case errors.Is(err, docker.ErrNotFound):
			line("Aucun conteneur pour cette app : elle n'a pas encore démarré.", true, true)
		case err != nil:
			line(err.Error(), true, true)
		default:
			line("", false, true)
		}
	}()
}

func (l *logStreams) stop(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if cancel, ok := l.cancels[id]; ok {
		cancel()
		delete(l.cancels, id)
	}
}

func (l *logStreams) stopAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for id, cancel := range l.cancels {
		cancel()
		delete(l.cancels, id)
	}
}
