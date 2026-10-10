// Package docker is a minimal client for the Docker Engine API over its Unix socket.
package docker

import (
	"archive/tar"
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
	"strconv"
	"strings"
	"time"
)

// DefaultHost is $DOCKER_HOST, the standard socket, or the first socket found among the usual desktop
// installations (OrbStack, Docker Desktop, Colima), which do not always link the standard path.
func DefaultHost() string {
	if h := os.Getenv("DOCKER_HOST"); h != "" {
		return h
	}
	candidates := []string{"/var/run/docker.sock"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			home+"/.orbstack/run/docker.sock",
			home+"/.docker/run/docker.sock",
			home+"/.colima/default/docker.sock",
		)
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return "unix://" + path
		}
	}
	return "unix:///var/run/docker.sock"
}

// Client talks to one Docker daemon.
type Client struct {
	http *http.Client
	path string // the Unix socket, dialed directly for interactive sessions
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
	return &Client{http: &http.Client{Transport: transport}, path: path}, nil
}

// Info is the part of GET /info the agent reports.
type Info struct {
	ServerVersion     string `json:"ServerVersion"`
	ContainersRunning int    `json:"ContainersRunning"`
	Name              string `json:"Name"`            // the host's name
	OperatingSystem   string `json:"OperatingSystem"` // the host's OS, even when asked from a container
	Architecture      string `json:"Architecture"`
	NCPU              int    `json:"NCPU"`
	MemTotal          uint64 `json:"MemTotal"`
	DockerRootDir     string `json:"DockerRootDir"` // where images and containers are stored on the host
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
	if body == nil {
		return c.doRaw(ctx, method, path, "", nil)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return c.doRaw(ctx, method, path, "application/json", bytes.NewReader(raw))
}

func (c *Client) doRaw(ctx context.Context, method, path, contentType string, body io.Reader) (*http.Response, error) {
	// The host part is ignored by the Unix dialer.
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
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

// Build builds an image from a Dockerfile alone, without other files: COPY and ADD of local files fail, the
// Dockerfile fetches what it needs (RUN git clone, ADD of a URL…). The base images are pulled again so
// that a rebuild picks up their updates. A failed build's error ends with the last lines of its output.
func (c *Client) Build(ctx context.Context, tag, dockerfile string) error {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "Dockerfile", Mode: 0o644, Size: int64(len(dockerfile)), ModTime: time.Unix(0, 0)}); err != nil {
		return err
	}
	if _, err := io.WriteString(tw, dockerfile); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	q := url.Values{"t": {tag}, "pull": {"1"}, "rm": {"1"}, "forcerm": {"1"}}
	resp, err := c.doRaw(ctx, http.MethodPost, "/build?"+q.Encode(), "application/x-tar", &buf)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// The body is a stream of JSON messages: output lines in "stream", failures in "error".
	var tail []string
	dec := json.NewDecoder(resp.Body)
	for {
		var msg struct {
			Stream string `json:"stream"`
			Error  string `json:"error"`
		}
		if err := dec.Decode(&msg); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		for _, line := range strings.Split(msg.Stream, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				tail = append(tail, line)
			}
		}
		if len(tail) > 8 {
			tail = tail[len(tail)-8:]
		}
		if msg.Error != "" {
			if len(tail) == 0 {
				return errors.New(msg.Error)
			}
			return fmt.Errorf("%s\n%s", msg.Error, strings.Join(tail, "\n"))
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
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Status    string `json:"Status"` // created, running, paused, restarting, removing, exited, dead
		Running   bool   `json:"Running"`
		Pid       int    `json:"Pid"` // the main process on the host, while running
		OOMKilled bool   `json:"OOMKilled"`
		ExitCode  int    `json:"ExitCode"`
		Error     string `json:"Error"`
		StartedAt string `json:"StartedAt"`
		// Health is set when the image declares a HEALTHCHECK.
		Health *struct {
			Status string `json:"Status"` // starting, healthy, unhealthy
		} `json:"Health"`
	} `json:"State"`
	RestartCount int `json:"RestartCount"`
	HostConfig   struct {
		Binds []string `json:"Binds"`
	} `json:"HostConfig"`
	NetworkSettings struct {
		Ports map[string][]struct {
			HostPort string `json:"HostPort"`
		} `json:"Ports"`
		Networks map[string]struct {
			IPAddress string   `json:"IPAddress"`
			Aliases   []string `json:"Aliases"`
			DNSNames  []string `json:"DNSNames"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

// Inspect returns a container by name or ID.
func (c *Client) Inspect(ctx context.Context, name string) (Container, error) {
	var ct Container
	return ct, c.get(ctx, "/containers/"+url.PathEscape(name)+"/json", &ct)
}

// RemoveVolume deletes a named volume; a missing one is not an error.
func (c *Client) RemoveVolume(ctx context.Context, name string) error {
	err := c.call(ctx, http.MethodDelete, "/volumes/"+url.PathEscape(name), nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// InspectRaw returns a container's whole inspection, to recreate it with the same settings.
func (c *Client) InspectRaw(ctx context.Context, name string) (map[string]any, error) {
	var raw map[string]any
	return raw, c.get(ctx, "/containers/"+url.PathEscape(name)+"/json", &raw)
}

// Stats is a one-shot sample of a running container's resource use.
type Stats struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs  int    `json:"online_cpus"`
	} `json:"cpu_stats"`
	MemoryStats struct {
		Usage uint64            `json:"usage"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
}

// MemoryUsed is the memory used without the page cache, as `docker stats` shows it.
func (s Stats) MemoryUsed() uint64 {
	cache := s.MemoryStats.Stats["inactive_file"] // cgroup v2
	if cache == 0 {
		cache = s.MemoryStats.Stats["total_inactive_file"] // cgroup v1
	}
	if cache > s.MemoryStats.Usage {
		return 0
	}
	return s.MemoryStats.Usage - cache
}

// Stats samples a container's resource use without waiting for a second sample: CPU use is a counter, so
// callers compare two samples.
func (c *Client) Stats(ctx context.Context, name string) (Stats, error) {
	var st Stats
	return st, c.get(ctx, "/containers/"+url.PathEscape(name)+"/stats?stream=false&one-shot=true", &st)
}

// Summary is a container as GET /containers/json lists it.
type Summary struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Image   string            `json:"Image"`
	State   string            `json:"State"`
	Status  string            `json:"Status"`
	Created int64             `json:"Created"`
	Labels  map[string]string `json:"Labels"`
	Ports   []struct {
		IP          string `json:"IP"`
		PrivatePort int    `json:"PrivatePort"`
		PublicPort  int    `json:"PublicPort"`
		Type        string `json:"Type"`
	} `json:"Ports"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string   `json:"IPAddress"`
			Aliases   []string `json:"Aliases"`
			DNSNames  []string `json:"DNSNames"` // API 1.44+: the names it answers to on this network
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

// Network is a Docker network, as listed.
type Network struct {
	ID       string `json:"Id"`
	Name     string `json:"Name"`
	Driver   string `json:"Driver"`
	Internal bool   `json:"Internal"`
	IPAM     struct {
		Config []struct {
			Subnet string `json:"Subnet"`
		} `json:"Config"`
	} `json:"IPAM"`
}

// RemoveNetwork deletes a network; a missing one is not an error.
func (c *Client) RemoveNetwork(ctx context.Context, name string) error {
	err := c.call(ctx, http.MethodDelete, "/networks/"+url.PathEscape(name), nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// ListNetworks returns the Docker networks of the node.
func (c *Client) ListNetworks(ctx context.Context) ([]Network, error) {
	var list []Network
	return list, c.get(ctx, "/networks", &list)
}

// Name is the container's name without the leading slash.
func (s Summary) Name() string {
	if len(s.Names) == 0 {
		return s.ID
	}
	return strings.TrimPrefix(s.Names[0], "/")
}

// ListAll returns every container, running or not.
func (c *Client) ListAll(ctx context.Context) ([]Summary, error) {
	var list []Summary
	return list, c.get(ctx, "/containers/json?all=1", &list)
}

// Wait blocks until a container stops.
func (c *Client) Wait(ctx context.Context, name string) error {
	return c.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(name)+"/wait", nil, nil)
}

// Output returns everything a stopped container wrote to stdout.
func (c *Client) Output(ctx context.Context, name string) (string, error) {
	resp, err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(name)+"/logs?stdout=1", nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") == "application/vnd.docker.raw-stream" {
		raw, err := io.ReadAll(resp.Body)
		return string(raw), err
	}
	var out bytes.Buffer
	if err := demux(resp.Body, &out, io.Discard); err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return out.String(), nil
}

// PutArchive extracts a tar archive into a container's filesystem at path; the container may be created
// but not started yet.
func (c *Client) PutArchive(ctx context.Context, container, path string, archive []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		"http://docker/containers/"+url.PathEscape(container)+"/archive?path="+url.QueryEscape(path), bytes.NewReader(archive))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-tar")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("docker put archive: %d %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

// Rename gives a container another name.
func (c *Client) Rename(ctx context.Context, name, newName string) error {
	return c.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(name)+"/rename?name="+url.QueryEscape(newName), nil, nil)
}

// Restart restarts a container.
func (c *Client) Restart(ctx context.Context, name string) error {
	return c.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(name)+"/restart?t=10", nil, nil)
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

// PathExists reports whether a file exists in a container, without running anything in it.
func (c *Client) PathExists(ctx context.Context, container, path string) (bool, error) {
	resp, err := c.do(ctx, http.MethodHead, "/containers/"+url.PathEscape(container)+"/archive?path="+url.QueryEscape(path), nil)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	resp.Body.Close()
	return true, nil
}

// RemoveImage deletes an image tag; Docker refuses while a container still uses it.
func (c *Client) RemoveImage(ctx context.Context, ref string) error {
	err := c.call(ctx, http.MethodDelete, "/images/"+ref, nil, nil)
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
	resp, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/containers/%s/logs?follow=1&stdout=1&stderr=1&timestamps=1&tail=%d",
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

// Exec is an interactive shell session in a container, with a TTY: what is written to it is typed in the
// shell, what is read from it is the terminal's output.
type Exec struct {
	c    *Client
	id   string
	conn net.Conn
	r    *bufio.Reader
}

func (e *Exec) Read(p []byte) (int, error)  { return e.r.Read(p) }
func (e *Exec) Write(p []byte) (int, error) { return e.conn.Write(p) }
func (e *Exec) Close() error                { return e.conn.Close() }

// Resize sets the terminal's size.
func (e *Exec) Resize(ctx context.Context, cols, rows uint32) error {
	return e.c.call(ctx, http.MethodPost, fmt.Sprintf("/exec/%s/resize?h=%d&w=%d", e.id, rows, cols), nil, nil)
}

// ExitCode returns the exit code of the shell once it ended.
func (e *Exec) ExitCode(ctx context.Context) (int, error) {
	var info struct {
		ExitCode int `json:"ExitCode"`
	}
	err := e.c.get(ctx, "/exec/"+e.id+"/json", &info)
	return info.ExitCode, err
}

// StartExec runs cmd in a container with a TTY and returns the session. Docker hands over the raw
// connection after a 101 Switching Protocols answer, which net/http's client does not support, so the
// request is written by hand on a dedicated connection.
func (c *Client) StartExec(ctx context.Context, container string, cmd []string, cols, rows uint32) (*Exec, error) {
	var created struct {
		ID string `json:"Id"`
	}
	if err := c.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(container)+"/exec", map[string]any{
		"AttachStdin": true, "AttachStdout": true, "AttachStderr": true, "Tty": true,
		"Cmd": cmd, "Env": []string{"TERM=xterm-256color"},
		"ConsoleSize": []uint32{rows, cols},
	}, &created); err != nil {
		return nil, err
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.path)
	if err != nil {
		return nil, err
	}
	body := `{"Detach":false,"Tty":true}`
	req := "POST /exec/" + created.ID + "/start HTTP/1.1\r\nHost: docker\r\nContent-Type: application/json\r\n" +
		"Connection: Upgrade\r\nUpgrade: tcp\r\nContent-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body
	if _, err := io.WriteString(conn, req); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols && resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		conn.Close()
		return nil, fmt.Errorf("docker exec: %d %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return &Exec{c: c, id: created.ID, conn: conn, r: br}, nil
}
