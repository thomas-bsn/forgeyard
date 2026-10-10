package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/coder/websocket"

	"github.com/thomas-bsn/forgeyard/internal/nodes"
)

// A terminal is a WebSocket: binary messages are keystrokes one way and terminal output the other way;
// text messages carry control frames, {"type":"resize","cols":…,"rows":…} from the browser and
// {"type":"exit","code":…,"error":…} from the server when the shell ends.

type terminalControl struct {
	Type  string `json:"type"`
	Cols  uint32 `json:"cols,omitempty"`
	Rows  uint32 `json:"rows,omitempty"`
	Code  int32  `json:"code,omitempty"`
	Error string `json:"error,omitempty"`
}

// handleAppTerminal opens a shell in an app's container.
func (s *Server) handleAppTerminal(w http.ResponseWriter, r *http.Request) {
	a, ok := s.appFromPath(w, r)
	if !ok {
		return
	}
	s.serveTerminal(w, r, a.NodeID, a.ID, "", func() {
		s.appEvent(a.ID, eventInfo, "Terminal ouvert par "+currentUser(r).DisplayName)
	})
}

// handleContainerTerminal opens a shell in an external container (superadmin only).
func (s *Server) handleContainerTerminal(w http.ResponseWriter, r *http.Request) {
	nodeID, c, ok := s.containerFromPath(w, r)
	if !ok {
		return
	}
	s.serveTerminal(w, r, nodeID, 0, c.GetId(), func() {
		s.containerEvent(nodeID, c.GetName(), eventInfo, "Terminal ouvert par "+currentUser(r).DisplayName)
	})
}

func (s *Server) serveTerminal(w http.ResponseWriter, r *http.Request, nodeID, appID int64, containerID string, opened func()) {
	cols, _ := strconv.Atoi(r.URL.Query().Get("cols"))
	rows, _ := strconv.Atoi(r.URL.Query().Get("rows"))
	if cols <= 0 || cols > 1000 {
		cols = 80
	}
	if rows <= 0 || rows > 500 {
		rows = 24
	}
	// Only pages of this instance may open a terminal: the session cookie alone is not enough, since a
	// WebSocket is not bound by the same-origin policy.
	var origins []string
	if base, err := s.publicURL(r.Context(), r); err == nil {
		if u, err := url.Parse(base); err == nil {
			origins = append(origins, u.Host)
		}
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: origins})
	if err != nil {
		return // Accept already answered
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1 << 20)

	sess, err := s.nodes.Exec(nodeID, appID, containerID, uint32(cols), uint32(rows))
	if errors.Is(err, nodes.ErrOffline) {
		s.terminalExit(r.Context(), conn, 0, "le node de ce conteneur est hors ligne")
		return
	}
	if err != nil {
		s.terminalExit(r.Context(), conn, 0, err.Error())
		return
	}
	defer sess.Close()
	opened()
	s.logger.Info("terminal opened", "node_id", nodeID, "app_id", appID, "container", containerID, "by", currentUser(r).DisplayName)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	// Browser → shell.
	go func() {
		defer cancel()
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				sess.Input(data)
				continue
			}
			var c terminalControl
			if json.Unmarshal(data, &c) == nil && c.Type == "resize" && c.Cols > 0 && c.Rows > 0 && c.Cols <= 1000 && c.Rows <= 500 {
				sess.Resize(c.Cols, c.Rows)
			}
		}
	}()
	// Shell → browser.
	for {
		select {
		case <-ctx.Done():
			return
		case out, ok := <-sess.Output:
			if !ok {
				s.terminalExit(ctx, conn, 0, "terminal fermé")
				return
			}
			if len(out.GetData()) > 0 {
				wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Write(wctx, websocket.MessageBinary, out.GetData())
				wcancel()
				if err != nil {
					return
				}
			}
			if out.GetClosed() {
				s.terminalExit(ctx, conn, out.GetExitCode(), out.GetError())
				return
			}
		}
	}
}

func (s *Server) terminalExit(ctx context.Context, conn *websocket.Conn, code int32, msg string) {
	raw, _ := json.Marshal(terminalControl{Type: "exit", Code: code, Error: msg})
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn.Write(wctx, websocket.MessageText, raw)
	conn.Close(websocket.StatusNormalClosure, "")
}
