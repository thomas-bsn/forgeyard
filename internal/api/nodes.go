package api

import (
	"database/sql"
	"errors"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/auth"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

const joinTokenTTL = time.Hour

var nodeNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

type nodeMetrics struct {
	CPUPercent        float64 `json:"cpuPercent"`
	MemoryUsedBytes   uint64  `json:"memoryUsedBytes"`
	DiskUsedBytes     uint64  `json:"diskUsedBytes"`
	ContainersRunning int32   `json:"containersRunning"`
}

type nodeResponse struct {
	ID            int64        `json:"id"`
	Name          string       `json:"name"`
	State         string       `json:"state"` // pending, online or offline
	Hostname      string       `json:"hostname"`
	OS            string       `json:"os"`
	Arch          string       `json:"arch"`
	CPUs          int64        `json:"cpus"`
	MemoryBytes   int64        `json:"memoryBytes"`
	DiskBytes     int64        `json:"diskBytes"`
	DockerVersion string       `json:"dockerVersion"`
	AgentVersion  string       `json:"agentVersion"`
	LastSeenAt    int64        `json:"lastSeenAt,omitempty"`
	Metrics       *nodeMetrics `json:"metrics,omitempty"`
	CPUHistory    []float64    `json:"cpuHistory,omitempty"`
}

func (s *Server) toNodeResponse(n db.Node) nodeResponse {
	resp := nodeResponse{
		ID: n.ID, Name: n.Name, State: "pending", Hostname: n.Hostname, OS: n.Os, Arch: n.Arch, CPUs: n.Cpus,
		MemoryBytes: n.MemoryBytes, DiskBytes: n.DiskBytes, DockerVersion: n.DockerVersion,
		AgentVersion: n.AgentVersion, LastSeenAt: n.LastSeenAt.Int64,
	}
	if n.Status != "active" {
		return resp
	}
	resp.State = "offline"
	live, ok := s.nodes.Live(n.ID)
	if !ok {
		return resp
	}
	resp.State = "online"
	if len(live.Metrics) > 0 {
		m := live.Metrics[len(live.Metrics)-1]
		resp.Metrics = &nodeMetrics{
			CPUPercent: m.GetCpuPercent(), MemoryUsedBytes: m.GetMemoryUsedBytes(),
			DiskUsedBytes: m.GetDiskUsedBytes(), ContainersRunning: m.GetContainersRunning(),
		}
		resp.LastSeenAt = m.GetUnixTime()
	}
	for _, m := range live.Metrics {
		resp.CPUHistory = append(resp.CPUHistory, m.GetCpuPercent())
	}
	return resp
}

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListNodes(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]nodeResponse, len(list))
	for i, n := range list {
		out[i] = s.toNodeResponse(n)
	}
	writeJSON(w, http.StatusOK, out)
}

type createNodeRequest struct {
	Name string `json:"name"`
}

type joinCommandResponse struct {
	Node        nodeResponse `json:"node"`
	Command     string       `json:"command"`
	ExpiresAt   int64        `json:"expiresAt"`
	AgentServer string       `json:"agentServer"`
}

func (s *Server) handleCreateNode(w http.ResponseWriter, r *http.Request) {
	var body createNodeRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	name := strings.ToLower(strings.TrimSpace(body.Name))
	if !nodeNamePattern.MatchString(name) {
		writeError(w, http.StatusBadRequest, "nom : 1 à 32 caractères parmi a-z, 0-9 et -, sans tiret au début")
		return
	}
	token := auth.NewToken()
	expires := time.Now().Add(joinTokenTTL)
	node, err := s.store.CreateNode(r.Context(), db.CreateNodeParams{
		Name:          name,
		JoinTokenHash: nullString(auth.HashToken(token)),
		JoinExpiresAt: nullInt(expires.Unix()),
		CreatedAt:     time.Now().Unix(),
	})
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		writeError(w, http.StatusConflict, "un node porte déjà ce nom")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.logger.Info("node created", "node", node.Name, "by", currentUser(r).DisplayName)
	s.writeJoinCommand(w, r, http.StatusCreated, node, token, expires)
}

// handleNewJoinCommand replaces the join token of a node that has not joined yet, e.g. after it expired.
func (s *Server) handleNewJoinCommand(w http.ResponseWriter, r *http.Request) {
	node, ok := s.nodeFromPath(w, r)
	if !ok {
		return
	}
	if node.Status != "pending" {
		writeError(w, http.StatusConflict, "ce node est déjà enregistré")
		return
	}
	token := auth.NewToken()
	expires := time.Now().Add(joinTokenTTL)
	if err := s.store.ResetNodeJoinToken(r.Context(), db.ResetNodeJoinTokenParams{
		JoinTokenHash: nullString(auth.HashToken(token)), JoinExpiresAt: nullInt(expires.Unix()), ID: node.ID,
	}); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.writeJoinCommand(w, r, http.StatusOK, node, token, expires)
}

func (s *Server) writeJoinCommand(w http.ResponseWriter, r *http.Request, status int, node db.Node, token string, expires time.Time) {
	base, err := s.publicURL(r.Context(), r)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	base = strings.TrimRight(base, "/")
	cmd := "forgeyard-agent join --server " + base + " --token " + token + " --ca " + s.ca.Fingerprint()
	writeJSON(w, status, joinCommandResponse{
		Node: s.toNodeResponse(node), Command: cmd, ExpiresAt: expires.Unix(), AgentServer: s.agentAddress(base),
	})
}

// agentAddress is the host:port agents dial: the public host with the agent port.
func (s *Server) agentAddress(publicURL string) string {
	host := "localhost"
	if u, err := url.Parse(publicURL); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	return net.JoinHostPort(host, s.agentPort)
}

func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	node, ok := s.nodeFromPath(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteNode(r.Context(), node.ID); err != nil {
		s.internalError(w, r, err)
		return
	}
	// Its certificate is now rejected: the hub checks every connection against the nodes table.
	s.nodes.Disconnect(node.ID)
	s.logger.Info("node removed", "node", node.Name, "by", currentUser(r).DisplayName)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) nodeFromPath(w http.ResponseWriter, r *http.Request) (db.Node, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "node introuvable")
		return db.Node{}, false
	}
	node, err := s.store.GetNode(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "node introuvable")
		return db.Node{}, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return db.Node{}, false
	}
	return node, true
}

// handleCACertificate serves the CA certificate, which agents check against the fingerprint in their join
// command before sending their token.
func (s *Server) handleCACertificate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Write(s.ca.CertPEM)
}

// JoinRequest is sent by `forgeyard-agent join`.
type JoinRequest struct {
	Token string `json:"token"`
	CSR   string `json:"csr"`
}

// JoinResponse gives the agent everything it needs to connect.
type JoinResponse struct {
	NodeID        int64  `json:"nodeId"`
	NodeName      string `json:"nodeName"`
	Certificate   string `json:"certificate"`
	CACertificate string `json:"caCertificate"`
	AgentServer   string `json:"agentServer"`
}

// handleJoinNode exchanges a one-time join token for a client certificate signed by the CA.
func (s *Server) handleJoinNode(w http.ResponseWriter, r *http.Request) {
	var body JoinRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	ip := clientIP(r)
	if !s.limiter.Allowed(ip) {
		writeError(w, http.StatusTooManyRequests, "trop de tentatives, réessayez dans quelques minutes")
		return
	}
	var node db.Node
	var certPEM []byte
	err := s.store.InTx(r.Context(), func(q *db.Queries) error {
		var err error
		node, err = q.GetPendingNodeByJoinToken(r.Context(), db.GetPendingNodeByJoinTokenParams{
			JoinTokenHash: nullString(auth.HashToken(body.Token)), JoinExpiresAt: nullInt(time.Now().Unix()),
		})
		if err != nil {
			return err
		}
		var serial string
		certPEM, serial, err = s.ca.SignNode([]byte(body.CSR), node.ID)
		if err != nil {
			return errBadCSR
		}
		return q.ActivateNode(r.Context(), db.ActivateNodeParams{CertSerial: nullString(serial), ID: node.ID})
	})
	if errors.Is(err, sql.ErrNoRows) {
		s.limiter.Fail(ip)
		writeError(w, http.StatusForbidden, "token invalide, expiré ou déjà utilisé : générez une nouvelle commande dans Nodes")
		return
	}
	if errors.Is(err, errBadCSR) {
		writeError(w, http.StatusBadRequest, "demande de certificat invalide")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	base, err := s.publicURL(r.Context(), r)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.logger.Info("node joined", "node", node.Name, "ip", ip)
	writeJSON(w, http.StatusOK, JoinResponse{
		NodeID: node.ID, NodeName: node.Name, Certificate: string(certPEM),
		CACertificate: string(s.ca.CertPEM), AgentServer: s.agentAddress(base),
	})
}

var errBadCSR = errors.New("invalid certificate request")
