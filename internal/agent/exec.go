package agent

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/docker"
)

// shells are looked for in this order: bash when the image has it, else a plain sh, wherever it is.
var shells = []string{"/bin/bash", "/usr/bin/bash", "/bin/sh", "/usr/bin/sh", "/bin/ash", "/busybox/sh"}

// errNoShell is shown in the terminal of an image without any shell.
const errNoShell = "cette image n'a pas de shell (image minimale, distroless ou scratch) : on ne peut pas y ouvrir de terminal, les logs restent disponibles"

// findShell picks the container's shell by looking at its files, without running anything in it.
func findShell(ctx context.Context, dc *docker.Client, container string) ([]string, error) {
	for _, sh := range shells {
		ok, err := dc.PathExists(ctx, container, sh)
		if err != nil {
			return nil, err
		}
		if ok {
			return []string{sh, "-l"}, nil
		}
	}
	return nil, errors.New(errNoShell)
}

// execSessions are the terminals the control plane opened in this node's containers.
type execSessions struct {
	mu       sync.Mutex
	sessions map[string]*docker.Exec
}

func newExecSessions() *execSessions {
	return &execSessions{sessions: map[string]*docker.Exec{}}
}

// start opens a terminal and streams its output until the shell ends or the control plane closes it.
func (x *execSessions) start(ctx context.Context, dc *docker.Client, ext *externals, req *agentpb.ExecStart, send func(*agentpb.AgentMessage)) {
	id := req.GetSessionId()
	out := func(m *agentpb.ExecOutput) {
		m.SessionId = id
		send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_ExecOutput{ExecOutput: m}})
	}
	go func() {
		name := containerName(req.GetAppId())
		if req.GetAppId() == 0 {
			// An external container: only those, never Forgeyard's own containers.
			c, err := ext.external(ctx, req.GetContainerId())
			if err != nil {
				out(&agentpb.ExecOutput{Closed: true, Error: "conteneur introuvable sur ce node"})
				return
			}
			name = c.ID
		}
		sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		shell, err := findShell(sctx, dc, name)
		if err != nil {
			cancel()
			out(&agentpb.ExecOutput{Closed: true, Error: err.Error()})
			return
		}
		e, err := dc.StartExec(sctx, name, shell, req.GetCols(), req.GetRows())
		cancel()
		if err != nil {
			out(&agentpb.ExecOutput{Closed: true, Error: "impossible d'ouvrir un shell : " + err.Error()})
			return
		}
		x.mu.Lock()
		x.sessions[id] = e
		x.mu.Unlock()
		defer func() {
			x.mu.Lock()
			delete(x.sessions, id)
			x.mu.Unlock()
			e.Close()
		}()

		buf := make([]byte, 32<<10)
		for {
			n, err := e.Read(buf)
			if n > 0 {
				out(&agentpb.ExecOutput{Data: append([]byte(nil), buf[:n]...)})
			}
			if err != nil {
				break
			}
		}
		code, _ := e.ExitCode(context.Background())
		out(&agentpb.ExecOutput{Closed: true, ExitCode: int32(code)})
	}()
}

func (x *execSessions) get(id string) *docker.Exec {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.sessions[id]
}

func (x *execSessions) input(m *agentpb.ExecInput) {
	if e := x.get(m.GetSessionId()); e != nil {
		e.Write(m.GetData())
	}
}

func (x *execSessions) resize(ctx context.Context, m *agentpb.ExecResize) {
	if e := x.get(m.GetSessionId()); e != nil {
		e.Resize(ctx, m.GetCols(), m.GetRows())
	}
}

// close ends a terminal: closing the connection hangs up the shell.
func (x *execSessions) close(id string) {
	if e := x.get(id); e != nil {
		e.Close()
	}
}

func (x *execSessions) closeAll() {
	x.mu.Lock()
	defer x.mu.Unlock()
	for _, e := range x.sessions {
		e.Close()
	}
}
