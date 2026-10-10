package api

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/auth"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

// LocalJoinFile is the file, in the join directory shared with the agent of the Compose project, through
// which Forgeyard enables its own machine as a node: the agent waits for it, joins with it and deletes it.
const LocalJoinFile = "agent.json"

// LocalHostProbeFile is where the local agent tells, before joining, whether this machine's ports 80 and 443
// are already taken.
const LocalHostProbeFile = "host.json"

// localNodeName is the name given to Forgeyard's own machine.
const localNodeName = "local"

// LocalJoin is the content of LocalJoinFile.
type LocalJoin struct {
	Server        string `json:"server"`
	AgentServer   string `json:"agentServer"`
	Token         string `json:"token"`
	CAFingerprint string `json:"ca"`
}

// localNodeSupported reports whether this server can enable its own machine as a node, i.e. it runs with
// the agent of the Compose project next to it.
func (s *Server) localNodeSupported() bool {
	return s.joinDir != ""
}

// localProbe reports what the local agent found on this machine before it joined: whether its ports 80 and
// 443 are taken ("busy", "free", or "" when unknown) and its local IP.
func (s *Server) localProbe() (webPorts, localIP string) {
	if s.joinDir == "" {
		return "", ""
	}
	raw, err := os.ReadFile(filepath.Join(s.joinDir, LocalHostProbeFile))
	if err != nil {
		return "", ""
	}
	var probe struct {
		WebPorts string `json:"webPorts"`
		LocalIP  string `json:"localIp"`
	}
	if json.Unmarshal(raw, &probe) != nil {
		return "", ""
	}
	if probe.WebPorts != "busy" && probe.WebPorts != "free" {
		probe.WebPorts = ""
	}
	if net.ParseIP(probe.LocalIP) == nil {
		probe.LocalIP = ""
	}
	return probe.WebPorts, probe.LocalIP
}

// setupIngress is how Forgeyard's own machine receives web traffic, chosen in the wizard.
type setupIngress struct {
	Mode     string `json:"mode"` // "traefik" or "proxy"
	HTTPPort int    `json:"httpPort"`
}

func (in setupIngress) validate() (setupIngress, error) {
	switch in.Mode {
	case "", "traefik":
		return setupIngress{Mode: "traefik", HTTPPort: 8090}, nil
	case "proxy":
		if in.HTTPPort == 0 {
			in.HTTPPort = 8090
		}
		if in.HTTPPort < 1 || in.HTTPPort > 65535 || in.HTTPPort == 8080 || in.HTTPPort == 8081 {
			return in, badRequest{"port HTTP invalide (et 8080/8081 sont pris par Forgeyard)"}
		}
		return in, nil
	}
	return in, badRequest{"mode de reverse proxy inconnu"}
}

// createLocalNode creates the pending node of Forgeyard's own machine, with its ingress, and returns the
// join token to hand to the local agent.
func (s *Server) createLocalNode(ctx context.Context, q *db.Queries, in setupIngress) (string, error) {
	node, token, _, err := s.newNode(ctx, q, localNodeName, true)
	if err != nil {
		return "", err
	}
	_, err = q.UpdateNodeIngress(ctx, db.UpdateNodeIngressParams{
		IngressMode: in.Mode, IngressHttpPort: int64(in.HTTPPort), ID: node.ID,
	})
	return token, err
}

// newNode creates a pending node with a fresh join token.
func (s *Server) newNode(ctx context.Context, q *db.Queries, name string, local bool) (db.Node, string, time.Time, error) {
	token := auth.NewToken()
	expires := time.Now().Add(joinTokenTTL)
	var isLocal int64
	if local {
		isLocal = 1
	}
	node, err := q.CreateNode(ctx, db.CreateNodeParams{
		Name:          name,
		JoinTokenHash: nullString(auth.HashToken(token)),
		JoinExpiresAt: nullInt(expires.Unix()),
		IsLocal:       isLocal,
		CreatedAt:     time.Now().Unix(),
	})
	return node, token, expires, err
}

// writeLocalJoin hands a join token to the local agent. The agent reaches the server by its Compose service
// name, so nothing goes out through the public address or the proxy in front of it.
func (s *Server) writeLocalJoin(token string) error {
	agentServer := "forgeyard:" + s.agentPort
	if u, err := url.Parse(s.localServerURL); err == nil && u.Hostname() != "" {
		agentServer = net.JoinHostPort(u.Hostname(), s.agentPort)
	}
	raw, err := json.Marshal(LocalJoin{
		Server: s.localServerURL, AgentServer: agentServer, Token: token, CAFingerprint: s.ca.Fingerprint(),
	})
	if err != nil {
		return err
	}
	// Write then rename, so the agent never reads a half-written file.
	tmp := filepath.Join(s.joinDir, "."+LocalJoinFile+".tmp")
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.joinDir, LocalJoinFile))
}
