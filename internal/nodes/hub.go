// Package nodes tracks the agents connected to the control plane and serves their gRPC stream.
package nodes

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/pki"
	"github.com/thomas-bsn/forgeyard/internal/store"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

const (
	// historyLen metrics samples are kept per node, about 5 minutes at the agent's 5 s interval.
	historyLen = 60
	// touchEvery bounds how often last_seen_at is written while a node streams metrics.
	touchEvery = time.Minute
)

// Live is the in-memory state of a connected node.
type Live struct {
	ConnectedAt time.Time
	Metrics     []*agentpb.Metrics // oldest first
}

type session struct {
	cancel      context.CancelFunc
	connectedAt time.Time
	metrics     []*agentpb.Metrics
	out         chan *agentpb.ServerMessage // the stream's only sender reads it
}

// AppStatus is the last state an agent reported for an app.
type AppStatus struct {
	NodeID     int64
	Status     *agentpb.AppStatus
	ReportedAt time.Time
}

// DesiredStateFunc computes what must run on a node.
type DesiredStateFunc func(ctx context.Context, nodeID int64) (*agentpb.DesiredState, error)

// Hub knows which nodes are connected. It implements the AgentService gRPC server.
type Hub struct {
	agentpb.UnimplementedAgentServiceServer

	store  *store.Store
	logger *slog.Logger

	// Desired computes the state sent to an agent when it connects and on Push. Set it before serving.
	Desired DesiredStateFunc

	mu         sync.Mutex
	sessions   map[int64]*session
	statuses   map[int64]AppStatus              // by app ID
	logStreams map[string]chan *agentpb.LogLine // by stream ID
}

// NewHub returns an empty Hub.
func NewHub(st *store.Store, logger *slog.Logger) *Hub {
	return &Hub{
		store: st, logger: logger, sessions: make(map[int64]*session),
		statuses: make(map[int64]AppStatus), logStreams: make(map[string]chan *agentpb.LogLine),
	}
}

// AppStatus returns the last state reported for an app, if any.
func (h *Hub) AppStatus(appID int64) (AppStatus, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.statuses[appID]
	return s, ok
}

// ForgetApp drops the status of a deleted app.
func (h *Hub) ForgetApp(appID int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.statuses, appID)
}

func (h *Hub) send(nodeID int64, msg *agentpb.ServerMessage) bool {
	h.mu.Lock()
	s, ok := h.sessions[nodeID]
	h.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case s.out <- msg:
		return true
	case <-time.After(5 * time.Second):
		h.logger.Warn("node not reading its stream, message dropped", "node_id", nodeID)
		return false
	}
}

// Push sends a node its desired state again, after an app on it changed. An offline node gets it when it
// reconnects.
func (h *Hub) Push(ctx context.Context, nodeID int64) error {
	h.mu.Lock()
	_, online := h.sessions[nodeID]
	h.mu.Unlock()
	if !online || h.Desired == nil {
		return nil
	}
	d, err := h.Desired(ctx, nodeID)
	if err != nil {
		return err
	}
	h.send(nodeID, &agentpb.ServerMessage{Msg: &agentpb.ServerMessage_DesiredState{DesiredState: d}})
	return nil
}

// ErrOffline means the node holding an app is not connected.
var ErrOffline = errors.New("node offline")

// Logs streams an app's container output from its node until ctx ends. The channel is closed when the
// stream ends.
func (h *Hub) Logs(ctx context.Context, nodeID, appID int64, tail int) (<-chan *agentpb.LogLine, error) {
	id := randomID()
	ch := make(chan *agentpb.LogLine, 256)
	h.mu.Lock()
	h.logStreams[id] = ch
	h.mu.Unlock()
	if !h.send(nodeID, &agentpb.ServerMessage{Msg: &agentpb.ServerMessage_StartLogs{
		StartLogs: &agentpb.StartLogs{StreamId: id, AppId: appID, Tail: int32(tail)},
	}}) {
		h.closeLogStream(id)
		return nil, ErrOffline
	}
	go func() {
		<-ctx.Done()
		h.send(nodeID, &agentpb.ServerMessage{Msg: &agentpb.ServerMessage_StopLogs{StopLogs: &agentpb.StopLogs{StreamId: id}}})
		h.closeLogStream(id)
	}()
	return ch, nil
}

func (h *Hub) closeLogStream(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ch, ok := h.logStreams[id]; ok {
		close(ch)
		delete(h.logStreams, id)
	}
}

func (h *Hub) deliverLog(line *agentpb.LogLine) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch, ok := h.logStreams[line.GetStreamId()]
	if !ok {
		return
	}
	select {
	case ch <- line:
	default: // the viewer is too slow: drop rather than block the node's stream
	}
}

func randomID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Live returns the state of a connected node; ok is false when it is offline.
func (h *Hub) Live(nodeID int64) (live Live, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.sessions[nodeID]
	if !ok {
		return Live{}, false
	}
	return Live{ConnectedAt: s.connectedAt, Metrics: append([]*agentpb.Metrics(nil), s.metrics...)}, true
}

// Disconnect closes the stream of a node, if connected. Used when a node is removed.
func (h *Hub) Disconnect(nodeID int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s, ok := h.sessions[nodeID]; ok {
		s.cancel()
		delete(h.sessions, nodeID)
	}
}

// Connect serves one agent's stream for as long as it stays connected.
func (h *Hub) Connect(stream agentpb.AgentService_ConnectServer) error {
	node, err := h.authenticate(stream.Context())
	if err != nil {
		return err
	}

	first, err := stream.Recv()
	if err != nil {
		return err
	}
	hello := first.GetHello()
	if hello == nil {
		return status.Error(codes.InvalidArgument, "the first message must be Hello")
	}
	info := hello.GetInfo()
	if err := h.store.UpdateNodeInfo(stream.Context(), db.UpdateNodeInfoParams{
		Hostname: info.GetHostname(), Os: info.GetOs(), Arch: info.GetArch(), Cpus: int64(info.GetCpus()),
		MemoryBytes: int64(info.GetMemoryBytes()), DiskBytes: int64(info.GetDiskBytes()),
		DockerVersion: info.GetDockerVersion(), AgentVersion: hello.GetAgentVersion(),
		LastSeenAt: sql.NullInt64{Int64: time.Now().Unix(), Valid: true}, ID: node.ID,
	}); err != nil {
		return status.Error(codes.Internal, "saving node info failed")
	}
	if err := stream.Send(&agentpb.ServerMessage{Msg: &agentpb.ServerMessage_Welcome{
		Welcome: &agentpb.Welcome{NodeId: node.ID, NodeName: node.Name},
	}}); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	sess := h.register(node.ID, cancel)
	defer h.unregister(node.ID, sess)
	h.logger.Info("node connected", "node", node.Name, "hostname", info.GetHostname())

	// The stream's only sender.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case m := <-sess.out:
				if err := stream.Send(m); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	if err := h.Push(ctx, node.ID); err != nil {
		h.logger.Error("computing the node's desired state failed", "node", node.Name, "err", err)
	}

	msgs := make(chan *agentpb.AgentMessage)
	errc := make(chan error, 1)
	go func() {
		for {
			msg, err := stream.Recv()
			if err != nil {
				errc <- err
				return
			}
			select {
			case msgs <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()

	lastTouch := time.Now()
	for {
		select {
		case <-ctx.Done():
			h.logger.Info("node disconnected", "node", node.Name)
			return status.Error(codes.Canceled, "disconnected by the control plane")
		case err := <-errc:
			h.logger.Info("node disconnected", "node", node.Name, "reason", err)
			h.touch(node.ID)
			return nil
		case msg := <-msgs:
			switch m := msg.GetMsg().(type) {
			case *agentpb.AgentMessage_Metrics:
				h.addMetrics(sess, m.Metrics)
				if time.Since(lastTouch) > touchEvery {
					h.touch(node.ID)
					lastTouch = time.Now()
				}
			case *agentpb.AgentMessage_AppStatuses:
				h.setStatuses(node.ID, m.AppStatuses.GetApps())
			case *agentpb.AgentMessage_LogLine:
				h.deliverLog(m.LogLine)
				if m.LogLine.GetEnd() {
					h.closeLogStream(m.LogLine.GetStreamId())
				}
			}
		}
	}
}

// authenticate maps the verified client certificate to an active node with the same certificate serial,
// so a removed node, or an old certificate of a re-joined node, is rejected.
func (h *Hub) authenticate(ctx context.Context) (db.Node, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return db.Node{}, status.Error(codes.Unauthenticated, "no peer")
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(tlsInfo.State.VerifiedChains) == 0 || len(tlsInfo.State.VerifiedChains[0]) == 0 {
		return db.Node{}, status.Error(codes.Unauthenticated, "client certificate required")
	}
	nodeID, serial, err := pki.NodeIdentity(tlsInfo.State.VerifiedChains[0][0])
	if err != nil {
		return db.Node{}, status.Error(codes.Unauthenticated, err.Error())
	}
	node, err := h.store.GetNode(ctx, nodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Node{}, status.Error(codes.PermissionDenied, "this node has been removed")
	}
	if err != nil {
		return db.Node{}, status.Error(codes.Internal, "node lookup failed")
	}
	if node.Status != "active" || node.CertSerial.String != serial {
		return db.Node{}, status.Error(codes.PermissionDenied, "certificate revoked")
	}
	return node, nil
}

func (h *Hub) register(nodeID int64, cancel context.CancelFunc) *session {
	h.mu.Lock()
	defer h.mu.Unlock()
	// A node reconnecting before its old stream timed out replaces it.
	if old, ok := h.sessions[nodeID]; ok {
		old.cancel()
	}
	s := &session{cancel: cancel, connectedAt: time.Now(), out: make(chan *agentpb.ServerMessage, 64)}
	h.sessions[nodeID] = s
	return s
}

func (h *Hub) unregister(nodeID int64, s *session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions[nodeID] == s {
		delete(h.sessions, nodeID)
	}
}

func (h *Hub) setStatuses(nodeID int64, list []*agentpb.AppStatus) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	for _, st := range list {
		h.statuses[st.GetAppId()] = AppStatus{NodeID: nodeID, Status: st, ReportedAt: now}
	}
}

func (h *Hub) addMetrics(s *session, m *agentpb.Metrics) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s.metrics = append(s.metrics, m)
	if len(s.metrics) > historyLen {
		s.metrics = s.metrics[len(s.metrics)-historyLen:]
	}
}

func (h *Hub) touch(nodeID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.store.TouchNode(ctx, db.TouchNodeParams{
		LastSeenAt: sql.NullInt64{Int64: time.Now().Unix(), Valid: true}, ID: nodeID,
	}); err != nil {
		h.logger.Warn("updating node last seen failed", "node_id", nodeID, "err", err)
	}
}
