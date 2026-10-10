package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/nodes"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
	"github.com/thomas-bsn/forgeyard/internal/version"
)

// Agents follow the server's version: an agent built from another commit is asked to replace itself with
// the agent image of the server's commit (published by CI as :sha-<commit>), when it connects and again
// every agentUpdateRetry while it differs. Agents that cannot replace themselves (Docker Compose, a
// binary run by hand) are left alone. Admins can also update an agent from the Nodes page.

const (
	settingAgentAutoUpdate = "agent_auto_update" // "0" turns it off; on by default
	agentUpdateRetry       = 15 * time.Minute
)

// agentImageFor is the agent image matching the server's commit, or the configured image (:latest) for
// a server built without one.
func (s *Server) agentImageFor() string {
	if version.Commit == "" {
		return s.agentImage
	}
	repo := s.agentImage
	if i := strings.LastIndexByte(repo, ':'); i > strings.LastIndexByte(repo, '/') {
		repo = repo[:i]
	}
	return repo + ":sha-" + version.Commit
}

func (s *Server) agentAutoUpdate(ctx context.Context) bool {
	v, err := s.store.GetSetting(ctx, settingAgentAutoUpdate)
	return err != nil || v != "0"
}

// agentOutdated reports whether a connected agent runs another version than the server's.
func agentOutdated(live nodes.Live) bool {
	return version.Commit != "" && live.AgentVersion != version.Commit
}

// autoUpdateAgent asks an outdated agent to update, at most once per agentUpdateRetry.
func (s *Server) autoUpdateAgent(nodeID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	live, online := s.nodes.Live(nodeID)
	if !online || !live.SelfUpdate || !agentOutdated(live) || !s.agentAutoUpdate(ctx) {
		return
	}
	s.mu.Lock()
	last := s.agentUpdateTried[nodeID]
	due := time.Since(last) >= agentUpdateRetry
	if due {
		s.agentUpdateTried[nodeID] = time.Now()
	}
	s.mu.Unlock()
	if !due {
		return
	}
	image := s.agentImageFor()
	if err := s.nodes.UpdateAgent(nodeID, image); err != nil {
		s.logger.Warn("asking an agent to update failed", "node_id", nodeID, "err", err)
		return
	}
	s.logger.Info("agent asked to update", "node_id", nodeID, "from", live.AgentVersion, "image", image)
}

// AgentUpdates retries the update of outdated agents until ctx ends.
func (s *Server) AgentUpdates(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		list, err := s.store.ListNodes(ctx)
		if err != nil {
			continue
		}
		for _, n := range list {
			s.autoUpdateAgent(n.ID)
		}
	}
}

// handleUpdateAgent updates a node's agent now, whatever the automatic setting.
func (s *Server) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	node, ok := s.nodeFromPath(w, r)
	if !ok {
		return
	}
	image := s.agentImageFor()
	err := s.nodes.UpdateAgent(node.ID, image)
	switch {
	case errors.Is(err, nodes.ErrOffline):
		writeError(w, http.StatusConflict, "ce node est hors ligne")
		return
	case errors.Is(err, nodes.ErrNoSelfUpdate):
		msg := "cet agent ne peut pas se remplacer lui-même : mettez-le à jour à la main"
		if node.IsLocal != 0 {
			msg = "l'agent de la machine de Forgeyard se met à jour avec elle : git pull && docker compose up -d --build"
		}
		writeError(w, http.StatusConflict, msg)
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	s.mu.Lock()
	s.agentUpdateTried[node.ID] = time.Now()
	s.mu.Unlock()
	s.logger.Info("agent update requested", "node", node.Name, "image", image, "by", currentUser(r).DisplayName)
	writeJSON(w, http.StatusAccepted, s.toNodeResponse(node))
}

type agentSettings struct {
	AutoUpdate    bool   `json:"autoUpdate"`
	ServerVersion string `json:"serverVersion"` // read only
}

func (s *Server) handleGetAgentSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, agentSettings{AutoUpdate: s.agentAutoUpdate(r.Context()), ServerVersion: version.Commit})
}

func (s *Server) handlePutAgentSettings(w http.ResponseWriter, r *http.Request) {
	var body agentSettings
	if !decodeJSON(w, r, &body) {
		return
	}
	v := "0"
	if body.AutoUpdate {
		v = "1"
	}
	ctx := r.Context()
	if err := s.store.InTx(ctx, func(q *db.Queries) error { return storeSettings(ctx, q, map[string]string{settingAgentAutoUpdate: v}) }); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.logger.Info("agent auto update changed", "on", body.AutoUpdate, "by", currentUser(r).DisplayName)
	s.handleGetAgentSettings(w, r)
}
