// Package agent runs on a node: it joins the control plane once, then keeps a stream open to report the
// machine and, later, run the containers the control plane asks for.
package agent

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/thomas-bsn/forgeyard/internal/pki"
)

const (
	keyFile    = "node.key"
	certFile   = "node.crt"
	caFile     = "ca.crt"
	configFile = "agent.json"
)

// Config is what join saves next to the node's certificate.
type Config struct {
	NodeID      int64  `json:"nodeId"`
	NodeName    string `json:"nodeName"`
	AgentServer string `json:"agentServer"`
}

// State is a joined node's identity on disk.
type State struct {
	Config
	TLS *tls.Config
}

// ErrNotJoined means the state directory holds no node identity yet.
var ErrNotJoined = errors.New("this machine has not joined a Forgeyard server: run `forgeyard-agent join` first")

// LoadState reads the identity saved by Join.
func LoadState(dir string) (*State, error) {
	raw, err := os.ReadFile(filepath.Join(dir, configFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotJoined
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", configFile, err)
	}
	pair, err := tls.LoadX509KeyPair(filepath.Join(dir, certFile), filepath.Join(dir, keyFile))
	if err != nil {
		return nil, err
	}
	caPEM, err := os.ReadFile(filepath.Join(dir, caFile))
	if err != nil {
		return nil, err
	}
	caCert, err := pki.ParseCertPEM(caPEM)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	return &State{Config: cfg, TLS: &tls.Config{
		Certificates: []tls.Certificate{pair},
		RootCAs:      pool,
		// The control plane's agent-port certificate always carries this name, whatever address is dialed.
		ServerName: pki.ServerName,
		MinVersion: tls.VersionTLS13,
	}}, nil
}

func saveState(dir string, cfg Config, keyPEM, certPEM, caPEM []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	files := []struct {
		name string
		data []byte
		perm os.FileMode
	}{
		{keyFile, keyPEM, 0o600},
		{certFile, certPEM, 0o644},
		{caFile, caPEM, 0o644},
		{configFile, append(raw, '\n'), 0o644}, // last: its presence marks a complete join
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.name), f.data, f.perm); err != nil {
			return err
		}
	}
	return nil
}
