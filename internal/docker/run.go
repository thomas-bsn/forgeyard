package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// The tools of backups (restic, pg_dump, pg_restore) run as short-lived containers or as commands in a
// container, their output read without a terminal, and a database dump streamed from one to the other.

// RunResult is how a short-lived container ended.
type RunResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// Run creates a container, starts it and waits until it ends, then removes it. When stdin is set, it is
// the container's standard input, closed at its end.
func (c *Client) Run(ctx context.Context, name string, config map[string]any, stdin io.Reader) (RunResult, error) {
	c.Remove(ctx, name) // a leftover of an interrupted run
	if stdin != nil {
		config["OpenStdin"], config["StdinOnce"], config["AttachStdin"] = true, true, true
	}
	if err := c.Create(ctx, name, config); err != nil {
		return RunResult{}, err
	}
	defer c.Remove(context.WithoutCancel(ctx), name)
	if stdin != nil {
		// Attached before the start, so that nothing written is lost.
		conn, err := c.hijack(ctx, "/containers/"+url.PathEscape(name)+"/attach?stream=1&stdin=1", "")
		if err != nil {
			return RunResult{}, err
		}
		defer conn.Close()
		if err := c.Start(ctx, name); err != nil {
			return RunResult{}, err
		}
		_, copyErr := io.Copy(conn, stdin)
		conn.CloseWrite() // the end of its input
		if copyErr != nil && !errors.Is(copyErr, net.ErrClosed) && !strings.Contains(copyErr.Error(), "broken pipe") {
			return RunResult{}, copyErr
		}
	} else if err := c.Start(ctx, name); err != nil {
		return RunResult{}, err
	}
	code, err := c.waitExit(ctx, name)
	if err != nil {
		return RunResult{}, err
	}
	var stdout, stderr bytes.Buffer
	if err := c.output(ctx, name, &stdout, &stderr); err != nil {
		return RunResult{}, err
	}
	return RunResult{ExitCode: code, Stdout: stdout.String(), Stderr: stderr.String()}, nil
}

// waitExit waits until a container stops and returns its exit code.
func (c *Client) waitExit(ctx context.Context, name string) (int, error) {
	resp, err := c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(name)+"/wait", nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var out struct {
		StatusCode int `json:"StatusCode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, err
	}
	return out.StatusCode, nil
}

// output copies everything a container wrote.
func (c *Client) output(ctx context.Context, name string, stdout, stderr io.Writer) error {
	resp, err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(name)+"/logs?stdout=1&stderr=1", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") == "application/vnd.docker.raw-stream" {
		_, err := io.Copy(stdout, resp.Body)
		return err
	}
	if err := demux(resp.Body, stdout, stderr); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// ExecStream runs a command in a running container without a terminal. Its standard output is read from
// the returned reader as it comes; wait, called once it is read to the end (or closed), gives the exit code
// and the end of the error output.
func (c *Client) ExecStream(ctx context.Context, container string, cmd []string) (io.ReadCloser, func() (int, string, error), error) {
	var created struct {
		ID string `json:"Id"`
	}
	if err := c.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(container)+"/exec", map[string]any{
		"AttachStdout": true, "AttachStderr": true, "Tty": false, "Cmd": cmd,
	}, &created); err != nil {
		return nil, nil, err
	}
	conn, err := c.hijack(ctx, "/exec/"+created.ID+"/start", `{"Detach":false,"Tty":false}`)
	if err != nil {
		return nil, nil, err
	}
	pr, pw := io.Pipe()
	var stderr tailBuffer
	done := make(chan error, 1)
	go func() {
		err := demux(conn, pw, &stderr)
		if errors.Is(err, io.EOF) {
			err = nil
		}
		pw.CloseWithError(err)
		conn.Close()
		done <- err
	}()
	wait := func() (int, string, error) {
		err := <-done
		if err != nil && !errors.Is(err, io.ErrClosedPipe) {
			return 0, stderr.String(), err
		}
		var info struct {
			Running  bool `json:"Running"`
			ExitCode int  `json:"ExitCode"`
		}
		if err := c.get(context.WithoutCancel(ctx), "/exec/"+created.ID+"/json", &info); err != nil {
			return 0, stderr.String(), err
		}
		if info.Running {
			return -1, stderr.String(), errors.New("la commande tourne encore")
		}
		return info.ExitCode, stderr.String(), nil
	}
	return pr, wait, nil
}

// ExecRun runs a command in a running container and returns its exit code and output.
func (c *Client) ExecRun(ctx context.Context, container string, cmd []string) (int, string, error) {
	stdout, wait, err := c.ExecStream(ctx, container, cmd)
	if err != nil {
		return 0, "", err
	}
	var out tailBuffer
	io.Copy(&out, stdout)
	stdout.Close()
	code, stderr, err := wait()
	return code, strings.TrimSpace(out.String() + "\n" + stderr), err
}

// hijack sends a request on a dedicated connection and hands it over once Docker switched protocols:
// net/http's client does not support it.
func (c *Client) hijack(ctx context.Context, path, body string) (*bufferedConn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.path)
	if err != nil {
		return nil, err
	}
	req := "POST " + path + " HTTP/1.1\r\nHost: docker\r\nContent-Type: application/json\r\n" +
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
		return nil, fmt.Errorf("docker %s: %d %s", strings.SplitN(path, "?", 2)[0], resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return &bufferedConn{Conn: conn, r: br}, nil
}

// bufferedConn reads what the response reader already buffered before the connection itself.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) { return b.r.Read(p) }

func (b *bufferedConn) CloseWrite() error {
	if cw, ok := b.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}

// tailBuffer keeps the last 16 KiB written to it: the end of a tool's output is what explains a failure.
type tailBuffer struct {
	buf []byte
}

const tailMax = 16 << 10

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > tailMax {
		t.buf = t.buf[len(t.buf)-tailMax:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.buf) }
