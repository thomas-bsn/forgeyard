package api

import (
	"errors"
	"net/http"
	"sort"
	"strconv"

	"github.com/thomas-bsn/forgeyard/internal/nodes"
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
				OwnerName: owner.DisplayName,
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
func (s *Server) containerFromPath(w http.ResponseWriter, r *http.Request) (int64, string, bool) {
	nodeID, err := strconv.ParseInt(r.PathValue("node"), 10, 64)
	if err == nil {
		if c, ok := s.nodes.ExternalContainer(nodeID, r.PathValue("container")); ok {
			return nodeID, c.GetId(), true
		}
	}
	writeError(w, http.StatusNotFound, "conteneur introuvable (ou son node est hors ligne)")
	return 0, "", false
}

func (s *Server) handleContainerAction(w http.ResponseWriter, r *http.Request) {
	nodeID, id, ok := s.containerFromPath(w, r)
	if !ok {
		return
	}
	action := r.PathValue("action")
	if action != "start" && action != "stop" && action != "restart" {
		writeError(w, http.StatusNotFound, "action inconnue")
		return
	}
	if err := s.nodes.ContainerAction(nodeID, id, action); errors.Is(err, nodes.ErrOffline) {
		writeError(w, http.StatusServiceUnavailable, "le node de ce conteneur est hors ligne")
		return
	}
	s.logger.Info("external container "+action, "node_id", nodeID, "container", id, "by", currentUser(r).DisplayName)
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleContainerLogs(w http.ResponseWriter, r *http.Request) {
	nodeID, id, ok := s.containerFromPath(w, r)
	if !ok {
		return
	}
	lines, err := s.nodes.Logs(r.Context(), nodeID, 0, id, logTail(r))
	s.streamLogs(w, r, lines, err)
}
