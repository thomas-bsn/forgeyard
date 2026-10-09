package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	IsLocal       bool         `json:"isLocal"`
	PublicIP      string       `json:"publicIp"`
	IngressMode   string       `json:"ingressMode"`
	IngressPort   int64        `json:"ingressHttpPort"`
	Metrics       *nodeMetrics `json:"metrics,omitempty"`
	CPUHistory    []float64    `json:"cpuHistory,omitempty"`
}

func (s *Server) toNodeResponse(n db.Node) nodeResponse {
	resp := nodeResponse{
		ID: n.ID, Name: n.Name, State: "pending", Hostname: n.Hostname, OS: n.Os, Arch: n.Arch, CPUs: n.Cpus,
		MemoryBytes: n.MemoryBytes, DiskBytes: n.DiskBytes, DockerVersion: n.DockerVersion,
		AgentVersion: n.AgentVersion, LastSeenAt: n.LastSeenAt.Int64,
		PublicIP: n.PublicIp, IngressMode: n.IngressMode, IngressPort: n.IngressHttpPort, IsLocal: n.IsLocal != 0,
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
	// Local enables Forgeyard's own machine: its agent joins by itself, no command to run.
	Local bool `json:"local"`
}

type joinCommandResponse struct {
	Node nodeResponse `json:"node"`
	// Command joins with the agent binary.
	Command string `json:"command"`
	// DockerCommand runs the agent as a container on another machine.
	DockerCommand string `json:"dockerCommand"`
	ExpiresAt     int64  `json:"expiresAt"`
	AgentServer   string `json:"agentServer"`
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
	if body.Local {
		if !s.localNodeSupported() {
			writeError(w, http.StatusBadRequest, "cette installation ne contient pas d'agent local (Forgeyard ne tourne pas avec docker compose)")
			return
		}
		if exists, err := s.store.HasLocalNode(r.Context()); err != nil {
			s.internalError(w, r, err)
			return
		} else if exists != 0 {
			writeError(w, http.StatusConflict, "cette machine est déjà un node")
			return
		}
	}
	node, token, expires, err := s.newNode(r.Context(), s.store.Queries, name, body.Local)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		writeError(w, http.StatusConflict, "un node porte déjà ce nom")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.logger.Info("node created", "node", node.Name, "local", body.Local, "by", currentUser(r).DisplayName)
	if body.Local {
		if err := s.writeLocalJoin(token); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
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
	if node.IsLocal != 0 && s.localNodeSupported() {
		if err := s.writeLocalJoin(token); err != nil {
			s.internalError(w, r, err)
			return
		}
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
	joinArgs := "--token " + token + " --ca " + s.ca.Fingerprint()
	docker := "docker run -d --name forgeyard-agent --restart unless-stopped \\\n" +
		"  -v /var/run/docker.sock:/var/run/docker.sock \\\n" +
		"  -v /:/host:ro -e FORGEYARD_HOST_ROOT=/host \\\n" +
		"  -v forgeyard-agent:/state \\\n" +
		"  " + s.agentImage + " \\\n" +
		"  run --server " + base + " " + joinArgs
	writeJSON(w, status, joinCommandResponse{
		Node:          s.toNodeResponse(node),
		Command:       "forgeyard-agent join --server " + base + " " + joinArgs,
		DockerCommand: docker,
		ExpiresAt:     expires.Unix(),
		AgentServer:   s.agentAddress(base),
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
	if apps, err := s.store.ListAppsByNode(r.Context(), node.ID); err != nil {
		s.internalError(w, r, err)
		return
	} else if len(apps) > 0 {
		writeError(w, http.StatusConflict, fmt.Sprintf("ce node héberge encore %d app(s) : supprimez-les d'abord", len(apps)))
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

type nodeIngressRequest struct {
	PublicIP string `json:"publicIp"`
	Mode     string `json:"ingressMode"`
	HTTPPort int    `json:"ingressHttpPort"`
}

// handleNodeIngress sets a node's public IP and how it receives web traffic. When the IP changes, the DNS
// records of its apps follow.
func (s *Server) handleNodeIngress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	node, ok := s.nodeFromPath(w, r)
	if !ok {
		return
	}
	var body nodeIngressRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	body.PublicIP = strings.TrimSpace(body.PublicIP)
	if body.PublicIP != "" && net.ParseIP(body.PublicIP) == nil {
		writeError(w, http.StatusBadRequest, "IP publique invalide")
		return
	}
	if body.Mode != "traefik" && body.Mode != "proxy" {
		writeError(w, http.StatusBadRequest, "mode inconnu")
		return
	}
	if body.Mode == "proxy" && (body.HTTPPort < 1 || body.HTTPPort > 65535 || body.HTTPPort == 8080 || body.HTTPPort == 8081) {
		writeError(w, http.StatusBadRequest, "port HTTP invalide (et 8080/8081 sont pris par Forgeyard)")
		return
	}
	if body.HTTPPort == 0 {
		body.HTTPPort = int(node.IngressHttpPort)
	}
	updated, err := s.store.UpdateNodeIngress(ctx, db.UpdateNodeIngressParams{
		PublicIp: body.PublicIP, IngressMode: body.Mode, IngressHttpPort: int64(body.HTTPPort), ID: node.ID,
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	c, err := s.loadDNSConfig(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if nodeIP(updated, c) != nodeIP(node, c) {
		if err := s.repointAppRecords(ctx, updated, c); err != nil {
			s.logger.Warn("updating DNS records after an IP change failed", "node", node.Name, "err", err)
		}
	}
	s.logger.Info("node ingress changed", "node", node.Name, "mode", body.Mode, "ip", body.PublicIP, "by", currentUser(r).DisplayName)
	s.push(ctx, node.ID)
	writeJSON(w, http.StatusOK, s.toNodeResponse(updated))
}

// repointAppRecords points the DNS records of a node's apps to its current IP.
func (s *Server) repointAppRecords(ctx context.Context, node db.Node, c dnsConfig) error {
	p, err := s.dnsProvider(c)
	if err != nil || p == nil {
		return err
	}
	apps, err := s.store.ListAppsByNode(ctx, node.ID)
	if err != nil {
		return err
	}
	for _, a := range apps {
		if a.DnsName == "" {
			continue
		}
		if err := s.setAppRecord(ctx, c, p, a.DnsName, nodeIP(node, c)); err != nil {
			return fmt.Errorf("%s: %w", a.DnsName, err)
		}
	}
	return nil
}
