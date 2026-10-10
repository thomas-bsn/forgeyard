package agent

import (
	"context"
	"sync"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/docker"
)

// shell starts bash when the image has it, else sh, as a login shell.
var shell = []string{"/bin/sh", "-c", "if command -v bash >/dev/null 2>&1; then exec bash -l; else exec sh -l; fi"}

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
