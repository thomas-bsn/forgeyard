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
	metricsInterval   = 5 * time.Second
	statusInterval    = 3 * time.Second
	externalsInterval = 10 * time.Second
	maxBackoff        = 30 * time.Second
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

	var ext *externals
	if dc != nil {
		ext = newExternals(dc)
	}
	sendExternals := func() {
		if ext == nil {
			return
		}
		list, err := ext.list(ctx)
		if err != nil {
			logger.Warn("listing external containers failed", "err", err)
			return
		}
		send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_ExternalContainers{
			ExternalContainers: &agentpb.ExternalContainers{Containers: list},
		}})
	}

	go func() {
		metrics := time.NewTicker(metricsInterval)
		statuses := time.NewTicker(statusInterval)
		externalsTick := time.NewTicker(externalsInterval)
		defer metrics.Stop()
		defer statuses.Stop()
		defer externalsTick.Stop()
		sendExternals()
		send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_Metrics{Metrics: sampleMetrics(ctx, dc)}})
		for {
			select {
			case <-ctx.Done():
				return
			case <-metrics.C:
				send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_Metrics{Metrics: sampleMetrics(ctx, dc)}})
			case <-externalsTick.C:
				sendExternals()
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
				logs.start(ctx, dc, ext, m.StartLogs, send)
			}
		case *agentpb.ServerMessage_StopLogs:
			logs.stop(m.StopLogs.GetStreamId())
		case *agentpb.ServerMessage_ContainerAction:
			if ext != nil {
				go func(a *agentpb.ContainerAction) {
					if err := ext.act(ctx, a); err != nil {
						logger.Warn("container action failed", "container", a.GetContainerId(), "action", a.GetAction(), "err", err)
					}
					sendExternals() // show the new state right away
				}(m.ContainerAction)
			}
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

func (l *logStreams) start(ctx context.Context, dc *docker.Client, ext *externals, req *agentpb.StartLogs, send func(*agentpb.AgentMessage)) {
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
		name := containerName(req.GetAppId())
		if req.GetAppId() == 0 {
			// An external container: only those, never Forgeyard's own containers.
			c, err := ext.external(ctx, req.GetContainerId())
			if err != nil {
				line("Conteneur introuvable sur ce node.", true, true)
				return
			}
			name = c.ID
		}
		err := dc.Logs(ctx, name, int(req.GetTail()), func(l docker.LogLine) {
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
