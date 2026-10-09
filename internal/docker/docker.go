// Package docker is a minimal client for the Docker Engine API over its Unix socket.
package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// DefaultHost is $DOCKER_HOST, or the standard socket.
func DefaultHost() string {
	if h := os.Getenv("DOCKER_HOST"); h != "" {
		return h
	}
	return "unix:///var/run/docker.sock"
}

// Client talks to one Docker daemon.
type Client struct {
	http *http.Client
}

// New returns a client for a unix:// Docker host.
func New(host string) (*Client, error) {
	path, ok := strings.CutPrefix(host, "unix://")
	if !ok {
		return nil, fmt.Errorf("unsupported Docker host %q: only unix:// sockets are supported", host)
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		},
	}
	return &Client{http: &http.Client{Transport: transport, Timeout: 30 * time.Second}}, nil
}

// Info is the part of GET /info the agent reports.
type Info struct {
	ServerVersion     string `json:"ServerVersion"`
	ContainersRunning int    `json:"ContainersRunning"`
}

// Info returns the daemon's version and container counts.
func (c *Client) Info(ctx context.Context) (Info, error) {
	var info Info
	return info, c.get(ctx, "/info", &info)
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	// The host part is ignored by the Unix dialer.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("docker %s: %d %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
