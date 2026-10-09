// Package docker is a minimal client for the Docker Engine API over its Unix socket.
package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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
	// No client timeout: pulls and log streams last long; callers bound requests with their context.
	return &Client{http: &http.Client{Transport: transport}}, nil
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

// ErrNotFound is returned for a missing container, image or network.
var ErrNotFound = errors.New("not found")

// apiError is the body of a Docker error response.
type apiError struct {
	Message string `json:"message"`
}

func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(raw)
	}
	// The host part is ignored by the Unix dialer.
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotModified {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		var e apiError
		json.Unmarshal(raw, &e)
		if e.Message == "" {
			e.Message = strings.TrimSpace(string(raw))
		}
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, e.Message)
		}
		return nil, fmt.Errorf("docker %s %s: %d %s", method, strings.SplitN(path, "?", 2)[0], resp.StatusCode, e.Message)
	}
	return resp, nil
}

func (c *Client) call(ctx context.Context, method, path string, body, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	resp, err := c.do(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.call(ctx, http.MethodGet, path, nil, out)
}

// EnsureNetwork creates a bridge network if it does not exist.
func (c *Client) EnsureNetwork(ctx context.Context, name string) error {
	err := c.get(ctx, "/networks/"+url.PathEscape(name), &struct{}{})
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	return c.call(ctx, http.MethodPost, "/networks/create", map[string]any{
		"Name": name, "Driver": "bridge", "CheckDuplicate": true,
	}, nil)
}

// Pull downloads an image, e.g. "nginx:1.27" or "ghcr.io/org/app:main". It returns once the pull is over.
func (c *Client) Pull(ctx context.Context, image string) error {
	ref := image
	if !strings.Contains(lastSegment(ref), ":") && !strings.Contains(ref, "@") {
		ref += ":latest" // without a tag Docker would pull every tag
	}
	resp, err := c.do(ctx, http.MethodPost, "/images/create?fromImage="+url.QueryEscape(ref), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// The body is a stream of JSON progress messages; failures arrive as an "error" message.
	dec := json.NewDecoder(resp.Body)
	for {
		var msg struct {
			Error string `json:"error"`
		}
		if err := dec.Decode(&msg); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		if msg.Error != "" {
			return errors.New(msg.Error)
		}
	}
}

func lastSegment(image string) string {
	if i := strings.LastIndex(image, "/"); i >= 0 {
		return image[i+1:]
	}
	return image
}

// Container is the part of a container's inspection the agent uses.
type Container struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Status    string `json:"Status"` // created, running, paused, restarting, removing, exited, dead
		Running   bool   `json:"Running"`
		OOMKilled bool   `json:"OOMKilled"`
		ExitCode  int    `json:"ExitCode"`
		Error     string `json:"Error"`
		StartedAt string `json:"StartedAt"`
	} `json:"State"`
	RestartCount    int `json:"RestartCount"`
	NetworkSettings struct {
		Ports map[string][]struct {
			HostPort string `json:"HostPort"`
		} `json:"Ports"`
	} `json:"NetworkSettings"`
}

// Inspect returns a container by name or ID.
func (c *Client) Inspect(ctx context.Context, name string) (Container, error) {
	var ct Container
	return ct, c.get(ctx, "/containers/"+url.PathEscape(name)+"/json", &ct)
}

// ListByLabel returns the names of all containers, running or not, carrying the label key.
func (c *Client) ListByLabel(ctx context.Context, key string) ([]string, error) {
	filters, _ := json.Marshal(map[string][]string{"label": {key}})
	var list []struct {
		Names []string `json:"Names"`
	}
	if err := c.get(ctx, "/containers/json?all=1&filters="+url.QueryEscape(string(filters)), &list); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(list))
	for _, ct := range list {
		if len(ct.Names) > 0 {
			names = append(names, strings.TrimPrefix(ct.Names[0], "/"))
		}
	}
	return names, nil
}

// Create creates a container from a Docker Engine API container config.
func (c *Client) Create(ctx context.Context, name string, config map[string]any) error {
	return c.call(ctx, http.MethodPost, "/containers/create?name="+url.QueryEscape(name), config, nil)
}

// Start starts a container; starting a running one is not an error.
func (c *Client) Start(ctx context.Context, name string) error {
	return c.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(name)+"/start", nil, nil)
}

// Stop stops a container, giving it 10 seconds to exit; stopping a stopped one is not an error.
func (c *Client) Stop(ctx context.Context, name string) error {
	return c.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(name)+"/stop?t=10", nil, nil)
}

// Remove force-removes a container and its anonymous volumes. A missing container is not an error.
func (c *Client) Remove(ctx context.Context, name string) error {
	err := c.call(ctx, http.MethodDelete, "/containers/"+url.PathEscape(name)+"?force=1&v=1", nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// LogLine is one line of a container's output.
type LogLine struct {
	Text   string
	Stderr bool
}

// Logs streams a container's output, starting with the last tail lines, until ctx ends or the container
// is removed. It calls fn for each line.
func (c *Client) Logs(ctx context.Context, name string, tail int, fn func(LogLine)) error {
	resp, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/containers/%s/logs?follow=1&stdout=1&stderr=1&tail=%d",
		url.PathEscape(name), tail), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Containers without a TTY multiplex stdout and stderr: each frame has an 8-byte header
	// (stream type, 3 zero bytes, big-endian payload size).
	if resp.Header.Get("Content-Type") == "application/vnd.docker.raw-stream" {
		return scanLines(resp.Body, false, fn)
	}
	stdoutR, stdoutW := io.Pipe()
	stderrR, stderrW := io.Pipe()
	done := make(chan error, 2)
	go func() { done <- scanLines(stdoutR, false, fn) }()
	go func() { done <- scanLines(stderrR, true, fn) }()
	err = demux(resp.Body, stdoutW, stderrW)
	stdoutW.Close()
	stderrW.Close()
	<-done
	<-done
	if errors.Is(err, io.EOF) || ctx.Err() != nil {
		return nil
	}
	return err
}

func demux(r io.Reader, stdout, stderr io.Writer) error {
	header := make([]byte, 8)
	for {
		if _, err := io.ReadFull(r, header); err != nil {
			return err
		}
		size := int64(binary.BigEndian.Uint32(header[4:]))
		dst := stdout
		if header[0] == 2 {
			dst = stderr
		}
		if _, err := io.CopyN(dst, r, size); err != nil {
			return err
		}
	}
}

func scanLines(r io.Reader, stderr bool, fn func(LogLine)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		fn(LogLine{Text: sc.Text(), Stderr: stderr})
	}
	return sc.Err()
}
