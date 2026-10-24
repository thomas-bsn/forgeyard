package nodes

import (
	"context"
	"errors"
	"github.com/thomas-bsn/forgeyard/internal/agentpb"
)

type pendingBindCheck struct {
	nodeID int64
	ch     chan *agentpb.BindValidationResult
}

// ValidateBindMounts is correlated to the target node; results from another node cannot satisfy it.
func (h *Hub) ValidateBindMounts(ctx context.Context, nodeID int64, mounts []*agentpb.BindMount, protected ...string) (*agentpb.BindValidationResult, error) {
	live, online := h.Live(nodeID)
	if !online {
		return nil, ErrOffline
	}
	if !live.BindMounts {
		return nil, errors.New("cet agent doit être mis à jour pour vérifier les bind mounts")
	}
	id := randomID()
	ch := make(chan *agentpb.BindValidationResult, 1)
	h.mu.Lock()
	h.bindChecks[id] = pendingBindCheck{nodeID, ch}
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.bindChecks, id); h.mu.Unlock() }()
	if !h.send(nodeID, &agentpb.ServerMessage{Msg: &agentpb.ServerMessage_ValidateBindMounts{ValidateBindMounts: &agentpb.ValidateBindMounts{Id: id, Mounts: mounts, ProtectedHostPaths: protected}}}) {
		return nil, ErrOffline
	}
	select {
	case res := <-ch:
		return res, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
