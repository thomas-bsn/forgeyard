package agent

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/pki"
)

// JoinOptions come from the command shown in the Nodes page.
type JoinOptions struct {
	Server        string // control plane URL, e.g. https://forgeyard.example.com
	Token         string // one-time join token
	CAFingerprint string // sha256:… of the control plane's CA, pinned before trusting it
	StateDir      string
}

// Join exchanges the token for a client certificate and saves the node identity in StateDir.
func Join(ctx context.Context, opts JoinOptions) (Config, error) {
	if _, err := os.Stat(filepath.Join(opts.StateDir, configFile)); err == nil {
		return Config{}, fmt.Errorf("this machine already joined a server (%s exists): remove it to join again", opts.StateDir)
	}
	server := strings.TrimRight(opts.Server, "/")
	httpClient := &http.Client{Timeout: 30 * time.Second}

	// Check the server is the one named in the join command before handing it the token: an impostor
	// (or a proxy in between) cannot present the pinned CA.
	caCert, err := fetchCA(ctx, httpClient, server)
	if err != nil {
		return Config{}, err
	}
	if got := pki.Fingerprint(caCert); got != opts.CAFingerprint {
		return Config{}, fmt.Errorf("the server's CA (%s) does not match --ca (%s): refusing to join", got, opts.CAFingerprint)
	}

	keyPEM, csrPEM, err := pki.NewNodeKey()
	if err != nil {
		return Config{}, err
	}
	body, _ := json.Marshal(map[string]string{"token": opts.Token, "csr": string(csrPEM)})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server+"/api/nodes/join", bytes.NewReader(body))
	if err != nil {
		return Config{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return Config{}, fmt.Errorf("contacting %s: %w", opts.Server, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return Config{}, errors.New(e.Error)
		}
		return Config{}, fmt.Errorf("server returned %d", resp.StatusCode)
	}
	var joined struct {
		NodeID        int64  `json:"nodeId"`
		NodeName      string `json:"nodeName"`
		Certificate   string `json:"certificate"`
		CACertificate string `json:"caCertificate"`
		AgentServer   string `json:"agentServer"`
	}
	if err := json.Unmarshal(raw, &joined); err != nil {
		return Config{}, fmt.Errorf("unexpected answer from the server: %w", err)
	}

	if joinedCA, err := pki.ParseCertPEM([]byte(joined.CACertificate)); err != nil || pki.Fingerprint(joinedCA) != opts.CAFingerprint {
		return Config{}, errors.New("the server answered with another CA than the one checked: refusing to join")
	}
	nodeCert, err := pki.ParseCertPEM([]byte(joined.Certificate))
	if err != nil {
		return Config{}, fmt.Errorf("node certificate: %w", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	if _, err := nodeCert.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return Config{}, fmt.Errorf("node certificate not signed by the CA: %w", err)
	}

	cfg := Config{NodeID: joined.NodeID, NodeName: joined.NodeName, AgentServer: joined.AgentServer}
	if err := saveState(opts.StateDir, cfg, keyPEM, []byte(joined.Certificate), []byte(joined.CACertificate)); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func fetchCA(ctx context.Context, c *http.Client, server string) (*x509.Certificate, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server+"/api/nodes/ca", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("contacting %s: %w", server, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s/api/nodes/ca returned %d: is this a Forgeyard server?", server, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	cert, err := pki.ParseCertPEM(raw)
	if err != nil {
		return nil, fmt.Errorf("CA certificate: %w", err)
	}
	return cert, nil
}
