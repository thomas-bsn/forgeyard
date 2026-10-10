package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
)

func TestTerminal(t *testing.T) {
	ts, server := newTestServerWithHandle(t)
	admin := newClient()
	post(t, admin, ts.URL+"/api/setup", setupRequest{Token: testToken, InstanceName: "F", Username: "boss",
		Password: "a-long-enough-password", PublicURL: "https://forgeyard.example.com"})
	fa := connectFakeAgent(t, ts.URL, server, admin, "node-a")
	fa.desired(t)
	var app appResponse
	postJSON(t, admin, ts.URL+"/api/apps", appInput{Name: "box", Image: "alpine", Port: 80}, &app)
	fa.desired(t)

	// The fake agent echoes what is typed, and ends the shell on "exit".
	go func() {
		for {
			msg, err := fa.stream.Recv()
			if err != nil {
				return
			}
			switch {
			case msg.GetExecStart() != nil:
				st := msg.GetExecStart()
				fa.stream.Send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_ExecOutput{ExecOutput: &agentpb.ExecOutput{
					SessionId: st.GetSessionId(), Data: []byte("$ "),
				}}})
			case msg.GetExecInput() != nil:
				in := msg.GetExecInput()
				out := &agentpb.ExecOutput{SessionId: in.GetSessionId(), Data: in.GetData()}
				if strings.Contains(string(in.GetData()), "exit") {
					out.Closed, out.ExitCode = true, 3
				}
				fa.stream.Send(&agentpb.AgentMessage{Msg: &agentpb.AgentMessage_ExecOutput{ExecOutput: out}})
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/apps/" + itoa(app.ID) + "/terminal?cols=100&rows=30"

	// Another site cannot open a terminal with the user's cookie.
	if _, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: admin, HTTPHeader: http.Header{"Origin": {"https://evil.example"}}}); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site terminal: %v", err)
	}

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: admin})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	read := func() (websocket.MessageType, string) {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return typ, string(data)
	}
	if _, got := read(); got != "$ " {
		t.Fatalf("prompt: %q", got)
	}
	conn.Write(ctx, websocket.MessageBinary, []byte("ls\n"))
	if _, got := read(); got != "ls\n" {
		t.Fatalf("echo: %q", got)
	}
	conn.Write(ctx, websocket.MessageBinary, []byte("exit\n"))
	read() // the echo of exit
	typ, got := read()
	if typ != websocket.MessageText || !strings.Contains(got, `"exit"`) || !strings.Contains(got, `"code":3`) {
		t.Fatalf("exit frame: %v %q", typ, got)
	}

	// Without a session, no terminal.
	if _, resp, err := websocket.Dial(ctx, wsURL, nil); err == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous terminal: %v", err)
	}
}
