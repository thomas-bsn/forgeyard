package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/thomas-bsn/forgeyard/internal/agent"
	"github.com/thomas-bsn/forgeyard/internal/agentpb"
)

// flagValue extracts "--name value" from a command line.
func flagValue(t *testing.T, cmd, name string) string {
	t.Helper()
	fields := strings.Fields(cmd)
	for i, f := range fields {
		if f == name && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	t.Fatalf("%s missing from %q", name, cmd)
	return ""
}

func TestNodeJoinConnectAndRemove(t *testing.T) {
	ts, server := newTestServerWithHandle(t)

	// Agent port, as main serves it.
	tlsConfig, err := server.ca.ServerTLS()
	if err != nil {
		t.Fatal(err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcServer := grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsConfig)))
	agentpb.RegisterAgentServiceServer(grpcServer, server.nodes)
	go grpcServer.Serve(lis)
	t.Cleanup(grpcServer.Stop)
	_, server.agentPort, _ = net.SplitHostPort(lis.Addr().String())

	admin := newClient()
	post(t, admin, ts.URL+"/api/setup", setupRequest{Token: testToken, InstanceName: "F", Username: "boss", Password: "a-long-enough-password"})

	b, _ := json.Marshal(createNodeRequest{Name: "node-a"})
	resp, err := admin.Post(ts.URL+"/api/admin/nodes", "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	var created joinCommandResponse
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated || created.Node.State != "pending" {
		t.Fatalf("create node: %d %+v", resp.StatusCode, created)
	}

	opts := agent.JoinOptions{
		Server:        flagValue(t, created.Command, "--server"),
		Token:         flagValue(t, created.Command, "--token"),
		CAFingerprint: flagValue(t, created.Command, "--ca"),
		StateDir:      filepath.Join(t.TempDir(), "agent"),
	}

	// A wrong CA fingerprint is refused before anything is saved.
	wrong := opts
	wrong.CAFingerprint = "sha256:00"
	if _, err := agent.Join(context.Background(), wrong); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("join with a wrong CA: %v", err)
	}
	// The token was never sent, so it still works; generating a new command replaces it anyway.
	resp, err = admin.Post(ts.URL+"/api/admin/nodes/"+itoa(created.Node.ID)+"/join-command", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("new join command: %d", resp.StatusCode)
	}
	opts.Token = flagValue(t, created.Command, "--token")

	if _, err := agent.Join(context.Background(), opts); err != nil {
		t.Fatal("join:", err)
	}
	if _, err := agent.Join(context.Background(), opts); err == nil {
		t.Fatal("joined twice into the same state directory")
	}

	st, err := agent.LoadState(opts.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- agent.Run(ctx, st, nil, slog.New(slog.NewTextHandler(io.Discard, nil))) }()

	var listed []nodeResponse
	deadline := time.Now().Add(10 * time.Second)
	for {
		get(t, admin, ts.URL+"/api/admin/nodes", &listed)
		if len(listed) == 1 && listed[0].State == "online" && listed[0].Metrics != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("node never came online: %+v", listed)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if listed[0].Hostname == "" || listed[0].CPUs == 0 || listed[0].AgentVersion != agent.Version {
		t.Fatalf("node info not reported: %+v", listed[0])
	}

	// Removing the node cuts the stream, and the agent stops instead of retrying forever.
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/admin/nodes/"+itoa(listed[0].ID), strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	resp, err = admin.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete node: %d", resp.StatusCode)
	}
	select {
	case err := <-done:
		if !errors.Is(err, agent.ErrRemoved) {
			t.Fatalf("agent stopped with %v, want ErrRemoved", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("agent kept running after its node was removed")
	}
}
