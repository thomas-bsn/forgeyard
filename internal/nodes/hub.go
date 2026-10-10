// Package nodes tracks the agents connected to the control plane and serves their gRPC stream.
package nodes

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"strconv"
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
	// An app's resource use is kept every usageEvery for usageLen samples: one hour.
	usageEvery = 15 * time.Second
	usageLen   = 240
)

// UsageSample is an app's resource use at one moment.
type UsageSample struct {
	At              int64   `json:"t"`
	CPUPercent      float64 `json:"cpu"`
	MemoryUsedBytes uint64  `json:"mem"`
}

// ExternalChangeFunc is told when an external container of a node changes state.
type ExternalChangeFunc func(nodeID int64, from, to *agentpb.ExternalContainer)

// ExternalKey identifies an external container across recreations: docker compose gives a recreated
// container a new ID but the same name.
func ExternalKey(nodeID int64, name string) string {
	return strconv.FormatInt(nodeID, 10) + "/" + name
}

// ExternalUsage returns an external container's resource use over the last hour, oldest first.
func (h *Hub) ExternalUsage(nodeID int64, name string) []UsageSample {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]UsageSample(nil), h.extUsage[ExternalKey(nodeID, name)]...)
}

func addSample(samples []UsageSample, now time.Time, cpu float64, mem uint64) []UsageSample {
	if n := len(samples); n > 0 && now.Unix()-samples[n-1].At < int64(usageEvery/time.Second) {
		return samples
	}
	samples = append(samples, UsageSample{At: now.Unix(), CPUPercent: cpu, MemoryUsedBytes: mem})
	if len(samples) > usageLen {
		samples = samples[len(samples)-usageLen:]
	}
	return samples
}

func (h *Hub) setExternals(nodeID int64, list []*agentpb.ExternalContainer) {
	type change struct{ from, to *agentpb.ExternalContainer }
	var changes []change
	now := time.Now()
	h.mu.Lock()
	prev := map[string]*agentpb.ExternalContainer{}
	for _, c := range h.externals[nodeID] {
		prev[c.GetName()] = c
	}
	_, known := h.externals[nodeID]
	for _, c := range list {
		key := ExternalKey(nodeID, c.GetName())
		if c.GetState() == "running" && c.GetMemoryUsedBytes() > 0 {
			h.extUsage[key] = addSample(h.extUsage[key], now, c.GetCpuPercent(), c.GetMemoryUsedBytes())
		}
		// The first list after a connection is the baseline: only later differences are changes.
		if p, ok := prev[c.GetName()]; known && (!ok || p.GetState() != c.GetState() || p.GetId() != c.GetId()) {
			changes = append(changes, change{p, c})
		}
	}
	h.externals[nodeID] = list
	h.mu.Unlock()
	if h.ExternalChanged != nil {
		for _, c := range changes {
			h.ExternalChanged(nodeID, c.from, c.to)
		}
	}
}

// StateChangeFunc is told when an app's reported state or restart count changes from a known previous
// report.
type StateChangeFunc func(appID int64, from, to *agentpb.AppStatus)

// Live is the in-memory state of a connected node.
type Live struct {
	ConnectedAt time.Time
	Metrics     []*agentpb.Metrics // oldest first
	// AgentVersion is the commit the agent was built from; SelfUpdate whether it can replace itself.
	AgentVersion string
	SelfUpdate   bool
	// UpdatingSince is when it was asked to update (zero when not), UpdateError why its last update failed.
	UpdatingSince time.Time
	UpdateError   string
}

type session struct {
	cancel        context.CancelFunc
	connectedAt   time.Time
	metrics       []*agentpb.Metrics
	out           chan *agentpb.ServerMessage // the stream's only sender reads it
	agentVersion  string
	selfUpdate    bool
	updatingSince time.Time
	updateError   string
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
	// StateChanged, if set, is called when an app's state changes. Set it before serving.
	StateChanged StateChangeFunc
	// ExternalChanged, if set, is called when an external container's state changes. Set it before serving.
	ExternalChanged ExternalChangeFunc
	// NodeChanged, if set, is called when a node connects or disconnects. Set it before serving.
	NodeChanged func(nodeID int64, online bool)

	mu         sync.Mutex
	sessions   map[int64]*session
	statuses   map[int64]AppStatus                    // by app ID
	usage      map[int64][]UsageSample                // by app ID, oldest first
	externals  map[int64][]*agentpb.ExternalContainer // by node ID, while connected
	extUsage   map[string][]UsageSample               // by ExternalKey, oldest first
	logStreams map[string]chan *agentpb.LogLine       // by stream ID
	execs      map[string]*execStream                 // terminals, by session ID
}

// NewHub returns an empty Hub.
func NewHub(st *store.Store, logger *slog.Logger) *Hub {
	return &Hub{
		store: st, logger: logger, sessions: make(map[int64]*session),
		statuses: make(map[int64]AppStatus), usage: make(map[int64][]UsageSample), extUsage: make(map[string][]UsageSample),
		externals: make(map[int64][]*agentpb.ExternalContainer), logStreams: make(map[string]chan *agentpb.LogLine),
		execs: make(map[string]*execStream),
	}
}

// AppStatus returns the last state reported for an app, if any.
func (h *Hub) AppStatus(appID int64) (AppStatus, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.statuses[appID]
	return s, ok
}

// AppUsage returns an app's resource use over the last hour, oldest first.
func (h *Hub) AppUsage(appID int64) []UsageSample {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]UsageSample(nil), h.usage[appID]...)
}

// ResetApp forgets what was reported for an app, as if pending: when it moves, the new node's first report
// is then a change, even when the app comes up at once.
func (h *Hub) ResetApp(appID int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.statuses[appID] = AppStatus{Status: &agentpb.AppStatus{AppId: appID, State: "pending"}, ReportedAt: time.Now()}
}

// ForgetApp drops the status of a deleted app.
func (h *Hub) ForgetApp(appID int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.statuses, appID)
	delete(h.usage, appID)
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

// Externals returns the external containers of every connected node, by node ID.
func (h *Hub) Externals() map[int64][]*agentpb.ExternalContainer {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[int64][]*agentpb.ExternalContainer, len(h.externals))
	for id, list := range h.externals {
		out[id] = list
	}
	return out
}

// ExternalContainer returns one external container of a connected node.
func (h *Hub) ExternalContainer(nodeID int64, containerID string) (*agentpb.ExternalContainer, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.externals[nodeID] {
		if c.GetId() == containerID {
			return c, true
		}
	}
	return nil, false
}

// ContainerAction asks a node to start, stop or restart one of its external containers.
func (h *Hub) ContainerAction(nodeID int64, containerID, action string) error {
	if !h.send(nodeID, &agentpb.ServerMessage{Msg: &agentpb.ServerMessage_ContainerAction{
		ContainerAction: &agentpb.ContainerAction{ContainerId: containerID, Action: action},
	}}) {
		return ErrOffline
	}
	return nil
}

// ErrOffline means the node holding an app is not connected.
var ErrOffline = errors.New("node offline")

// Logs streams an app's container output from its node until ctx ends. The channel is closed when the
// stream ends.
// containerID names an external container instead, when appID is 0.
func (h *Hub) Logs(ctx context.Context, nodeID, appID int64, containerID string, tail int) (<-chan *agentpb.LogLine, error) {
	id := randomID()
	ch := make(chan *agentpb.LogLine, 256)
	h.mu.Lock()
	h.logStreams[id] = ch
	h.mu.Unlock()
	if !h.send(nodeID, &agentpb.ServerMessage{Msg: &agentpb.ServerMessage_StartLogs{
		StartLogs: &agentpb.StartLogs{StreamId: id, AppId: appID, ContainerId: containerID, Tail: int32(tail)},
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
	return Live{
		ConnectedAt: s.connectedAt, Metrics: append([]*agentpb.Metrics(nil), s.metrics...),
		AgentVersion: s.agentVersion, SelfUpdate: s.selfUpdate, UpdatingSince: s.updatingSince, UpdateError: s.updateError,
	}, true
}

// ErrNoSelfUpdate is returned for an agent that cannot replace its own container.
var ErrNoSelfUpdate = errors.New("this agent cannot update itself")

// UpdateAgent asks a node's agent to replace itself with image. The agent reconnects with its new version,
// or reports why it could not.
func (h *Hub) UpdateAgent(nodeID int64, image string) error {
	h.mu.Lock()
	s, ok := h.sessions[nodeID]
	if ok && s.selfUpdate {
		s.updatingSince, s.updateError = time.Now(), ""
	}
	h.mu.Unlock()
	switch {
	case !ok:
		return ErrOffline
	case !s.selfUpdate:
		return ErrNoSelfUpdate
	}
	if !h.send(nodeID, &agentpb.ServerMessage{Msg: &agentpb.ServerMessage_UpdateAgent{UpdateAgent: &agentpb.UpdateAgent{Image: image}}}) {
		return ErrOffline
	}
	return nil
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
		DockerVersion: info.GetDockerVersion(), AgentVersion: hello.GetAgentVersion(), LocalIp: info.GetLocalIp(), DockerError: info.GetDockerError(),
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
	h.mu.Lock()
	sess.agentVersion, sess.selfUpdate = hello.GetAgentVersion(), hello.GetSelfUpdate()
	h.mu.Unlock()
	defer func() {
		if h.unregister(node.ID, sess) && h.NodeChanged != nil {
			h.NodeChanged(node.ID, false)
		}
	}()
	if h.NodeChanged != nil {
		h.NodeChanged(node.ID, true)
	}
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
			case *agentpb.AgentMessage_ExecOutput:
				h.deliverExec(m.ExecOutput)
			case *agentpb.AgentMessage_UpdateFailed:
				h.logger.Warn("agent update failed", "node", node.Name, "err", m.UpdateFailed.GetError())
				h.mu.Lock()
				sess.updatingSince, sess.updateError = time.Time{}, m.UpdateFailed.GetError()
				h.mu.Unlock()
			case *agentpb.AgentMessage_ExternalContainers:
				h.setExternals(node.ID, m.ExternalContainers.GetContainers())
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

// unregister forgets a node's session; it reports whether the node is now offline (a newer session of the
// same node may have replaced it).
func (h *Hub) unregister(nodeID int64, s *session) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions[nodeID] != s {
		return false
	}
	delete(h.sessions, nodeID)
	delete(h.externals, nodeID)
	return true
}

func (h *Hub) setStatuses(nodeID int64, list []*agentpb.AppStatus) {
	type change struct{ from, to *agentpb.AppStatus }
	var changes []change
	h.mu.Lock()
	now := time.Now()
	for _, st := range list {
		id := st.GetAppId()
		// A restart by Docker may happen between two reports, leaving the state unchanged: the restart
		// count tells it.
		if prev, ok := h.statuses[id]; ok && (prev.Status.GetState() != st.GetState() || prev.Status.GetRestartCount() != st.GetRestartCount()) {
			changes = append(changes, change{prev.Status, st})
		}
		h.statuses[id] = AppStatus{NodeID: nodeID, Status: st, ReportedAt: now}
		// No memory means no measure (an older agent, or Docker could not read the container's cgroup).
		if st.GetState() == "running" && st.GetMemoryUsedBytes() > 0 {
			h.usage[id] = addSample(h.usage[id], now, st.GetCpuPercent(), st.GetMemoryUsedBytes())
		}
	}
	h.mu.Unlock()
	if h.StateChanged != nil {
		for _, c := range changes {
			h.StateChanged(c.to.GetAppId(), c.from, c.to)
		}
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

// ExecSession is a terminal opened in a container of a node.
type ExecSession struct {
	h      *Hub
	nodeID int64
	id     string
	// Output carries what the shell prints, then a last message with Closed set; it is closed after.
	Output <-chan *agentpb.ExecOutput
}

// Exec opens a terminal in an app's container, or in an external container when appID is 0.
func (h *Hub) Exec(nodeID, appID int64, containerID string, cols, rows uint32) (*ExecSession, error) {
	id := randomID()
	st := &execStream{ch: make(chan *agentpb.ExecOutput, 1024)}
	h.mu.Lock()
	h.execs[id] = st
	h.mu.Unlock()
	if !h.send(nodeID, &agentpb.ServerMessage{Msg: &agentpb.ServerMessage_ExecStart{ExecStart: &agentpb.ExecStart{
		SessionId: id, AppId: appID, ContainerId: containerID, Cols: cols, Rows: rows,
	}}}) {
		h.endExec(id)
		return nil, ErrOffline
	}
	return &ExecSession{h: h, nodeID: nodeID, id: id, Output: st.ch}, nil
}

// Input types into the terminal.
func (s *ExecSession) Input(data []byte) {
	s.h.send(s.nodeID, &agentpb.ServerMessage{Msg: &agentpb.ServerMessage_ExecInput{ExecInput: &agentpb.ExecInput{SessionId: s.id, Data: data}}})
}

// Resize sets the terminal's size.
func (s *ExecSession) Resize(cols, rows uint32) {
	s.h.send(s.nodeID, &agentpb.ServerMessage{Msg: &agentpb.ServerMessage_ExecResize{ExecResize: &agentpb.ExecResize{SessionId: s.id, Cols: cols, Rows: rows}}})
}

// Close hangs up the shell.
func (s *ExecSession) Close() {
	s.h.send(s.nodeID, &agentpb.ServerMessage{Msg: &agentpb.ServerMessage_ExecClose{ExecClose: &agentpb.ExecClose{SessionId: s.id}}})
	s.h.endExec(s.id)
}

// execStream is a terminal's output channel; its own lock lets a delivery wait for a slow viewer without
// holding the hub, and never send on the channel once it is closed.
type execStream struct {
	mu     sync.Mutex
	ch     chan *agentpb.ExecOutput
	closed bool
}

func (st *execStream) close() {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.closed {
		st.closed = true
		close(st.ch)
	}
}

func (h *Hub) endExec(id string) {
	h.mu.Lock()
	st, ok := h.execs[id]
	delete(h.execs, id)
	h.mu.Unlock()
	if ok {
		st.close()
	}
}

// deliverExec passes a terminal's output on. A terminal must not lose output, so a slow viewer gets a few
// seconds before its session is dropped.
func (h *Hub) deliverExec(m *agentpb.ExecOutput) {
	h.mu.Lock()
	st, ok := h.execs[m.GetSessionId()]
	h.mu.Unlock()
	if !ok {
		return
	}
	st.mu.Lock()
	sent := false
	if !st.closed {
		select {
		case st.ch <- m:
			sent = true
		case <-time.After(5 * time.Second):
			h.logger.Warn("terminal viewer too slow, closing it", "session", m.GetSessionId())
		}
	}
	st.mu.Unlock()
	if !sent || m.GetClosed() {
		h.endExec(m.GetSessionId())
	}
}
