package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/nodes"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

// External containers are the containers of a node that Forgeyard did not create. They are listed with the
// apps, owned by the superadmin, who alone can act on them.

type containerResponse struct {
	NodeID         int64    `json:"nodeId"`
	NodeName       string   `json:"nodeName"`
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Image          string   `json:"image"`
	State          string   `json:"state"`
	Status         string   `json:"status"`
	CreatedAt      int64    `json:"createdAt"`
	Ports          []string `json:"ports"`
	CPUPercent     float64  `json:"cpuPercent,omitempty"`
	MemoryUsed     uint64   `json:"memoryUsedBytes,omitempty"`
	ComposeProject string   `json:"composeProject,omitempty"`
	OwnerName      string   `json:"ownerName"`
	LogoURL        string   `json:"logoUrl,omitempty"`
}

func (s *Server) handleListContainers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodeList, err := s.store.ListNodes(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	names := map[int64]string{}
	for _, n := range nodeList {
		names[n.ID] = n.Name
	}
	owner, err := s.store.GetSuperadmin(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := []containerResponse{}
	for nodeID, list := range s.nodes.Externals() {
		for _, c := range list {
			ports := c.GetPorts()
			if ports == nil {
				ports = []string{}
			}
			out = append(out, containerResponse{
				NodeID: nodeID, NodeName: names[nodeID], ID: c.GetId(), Name: c.GetName(), Image: c.GetImage(),
				State: c.GetState(), Status: c.GetStatus(), CreatedAt: c.GetCreatedAt(), Ports: ports,
				CPUPercent: c.GetCpuPercent(), MemoryUsed: c.GetMemoryUsedBytes(), ComposeProject: c.GetComposeProject(),
				OwnerName: owner.DisplayName, LogoURL: imageLogoURL(c.GetImage()),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NodeName != out[j].NodeName {
			return out[i].NodeName < out[j].NodeName
		}
		return out[i].Name < out[j].Name
	})
	writeJSON(w, http.StatusOK, out)
}

// containerFromPath finds the external container named in the path, on a connected node.
func (s *Server) containerFromPath(w http.ResponseWriter, r *http.Request) (int64, *agentpb.ExternalContainer, bool) {
	nodeID, err := strconv.ParseInt(r.PathValue("node"), 10, 64)
	if err == nil {
		if c, ok := s.nodes.ExternalContainer(nodeID, r.PathValue("container")); ok {
			return nodeID, c, true
		}
	}
	writeError(w, http.StatusNotFound, "conteneur introuvable (ou son node est hors ligne)")
	return 0, nil, false
}

// exitCode reads the exit code from Docker's status of a stopped container ("Exited (1) 2 hours ago").
func exitCode(c *agentpb.ExternalContainer) int {
	var code int
	if _, err := fmt.Sscanf(c.GetStatus(), "Exited (%d)", &code); err != nil {
		return 0
	}
	return code
}

// onExternalChange records the state changes of external containers as events.
func (s *Server) onExternalChange(nodeID int64, from, to *agentpb.ExternalContainer) {
	kind, msg, ok := externalChangeEvent(from, to)
	if ok {
		s.containerEvent(nodeID, to.GetName(), kind, msg)
	}
}

func externalChangeEvent(from, to *agentpb.ExternalContainer) (kind, msg string, ok bool) {
	switch {
	case from == nil:
		kind, msg = eventInfo, "Apparu sur le node ("+to.GetImage()+")"
	case from.GetId() != to.GetId():
		kind, msg = eventInfo, "Recréé ("+to.GetImage()+")"
	case to.GetState() == "running":
		kind, msg = eventSuccess, "En ligne"
	case to.GetState() == "exited" && exitCode(to) != 0:
		kind, msg = eventError, fmt.Sprintf("Planté (code %d)", exitCode(to))
	case to.GetState() == "exited":
		kind, msg = eventInfo, "Arrêté"
	case to.GetState() == "restarting":
		kind, msg = eventWarning, "Redémarre en boucle"
	case to.GetState() == "paused":
		kind, msg = eventInfo, "En pause"
	case to.GetState() == "dead":
		kind, msg = eventError, "Mort"
	default:
		return "", "", false
	}
	return kind, msg, true
}

// containerEvent records what happened to an external container. It never fails the caller.
func (s *Server) containerEvent(nodeID int64, name, kind, msg string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := s.store.AddContainerEvent(ctx, db.AddContainerEventParams{NodeID: nodeID, Name: name, At: time.Now().Unix(), Kind: kind, Message: msg})
	if err == nil {
		err = s.store.PruneContainerEvents(ctx, db.PruneContainerEventsParams{NodeID: nodeID, Name: name})
	}
	if err != nil {
		s.logger.Warn("recording a container event failed", "node_id", nodeID, "container", name, "err", err)
	}
}

func (s *Server) handleContainerEvents(w http.ResponseWriter, r *http.Request) {
	nodeID, c, ok := s.containerFromPath(w, r)
	if !ok {
		return
	}
	rows, err := s.store.ListContainerEvents(r.Context(), db.ListContainerEventsParams{NodeID: nodeID, Name: c.GetName(), Limit: 50})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]appEventResponse, 0, len(rows))
	for _, e := range rows {
		out = append(out, appEventResponse{At: e.At, Kind: e.Kind, Message: e.Message})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleContainerUsage(w http.ResponseWriter, r *http.Request) {
	nodeID, c, ok := s.containerFromPath(w, r)
	if !ok {
		return
	}
	samples := s.nodes.ExternalUsage(nodeID, c.GetName())
	if samples == nil {
		samples = []nodes.UsageSample{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"memoryLimitBytes": 0, "samples": samples})
}

func (s *Server) handleContainerAction(w http.ResponseWriter, r *http.Request) {
	nodeID, c, ok := s.containerFromPath(w, r)
	if !ok {
		return
	}
	id := c.GetId()
	action := r.PathValue("action")
	if action != "start" && action != "stop" && action != "restart" {
		writeError(w, http.StatusNotFound, "action inconnue")
		return
	}
	if err := s.nodes.ContainerAction(nodeID, id, action); errors.Is(err, nodes.ErrOffline) {
		writeError(w, http.StatusServiceUnavailable, "le node de ce conteneur est hors ligne")
		return
	}
	verb := map[string]string{"start": "Démarrage", "stop": "Arrêt", "restart": "Redémarrage"}[action]
	s.containerEvent(nodeID, c.GetName(), eventInfo, verb+" demandé par "+currentUser(r).DisplayName)
	s.logger.Info("external container "+action, "node_id", nodeID, "container", id, "by", currentUser(r).DisplayName)
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleContainerLogs(w http.ResponseWriter, r *http.Request) {
	nodeID, c, ok := s.containerFromPath(w, r)
	if !ok {
		return
	}
	lines, err := s.nodes.Logs(r.Context(), nodeID, 0, c.GetId(), logTail(r))
	s.streamLogs(w, r, lines, err)
}
