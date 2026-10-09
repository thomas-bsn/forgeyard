// Command agent runs on each node and executes the control plane's orders on the local Docker daemon.
//
//	forgeyard-agent join --server URL --token TOKEN --ca sha256:…   join once, then run
//	forgeyard-agent run [--server URL --token TOKEN --ca sha256:…]  run, joining first if needed
//
// The second form suits containers: the same command works on first start and on every restart.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/thomas-bsn/forgeyard/internal/agent"
	"github.com/thomas-bsn/forgeyard/internal/docker"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if len(os.Args) < 2 {
		usage()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "join":
		err = start(ctx, os.Args[2:], true, logger)
	case "run":
		err = start(ctx, os.Args[2:], false, logger)
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "erreur :", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage : forgeyard-agent join|run [--server URL --token TOKEN --ca FINGERPRINT]")
	os.Exit(2)
}

func defaultStateDir() string {
	if dir := os.Getenv("FORGEYARD_AGENT_STATE"); dir != "" {
		return dir
	}
	return "forgeyard-agent-data"
}

// start joins the server when asked to (join) or when the node has no identity yet and join flags are
// given (run), then runs the agent.
func start(ctx context.Context, args []string, mustJoin bool, logger *slog.Logger) error {
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	server := fs.String("server", "", "URL of the Forgeyard server")
	token := fs.String("token", "", "one-time join token")
	ca := fs.String("ca", "", "fingerprint of the server's CA (sha256:…)")
	agentServer := fs.String("agent-server", "", "host:port of the agent port, when it differs from the server's public address (e.g. forgeyard:8081 in the same Docker Compose project)")
	stateDir := fs.String("state-dir", defaultStateDir(), "where the node identity is stored")
	dockerHost := fs.String("docker-host", docker.DefaultHost(), "Docker daemon socket")
	fs.Parse(args)

	_, err := agent.LoadState(*stateDir)
	joined := err == nil
	if err != nil && !errors.Is(err, agent.ErrNotJoined) {
		return err
	}
	switch {
	case mustJoin && joined:
		return fmt.Errorf("this machine already joined a server (%s exists): remove it to join again", *stateDir)
	case !joined && (*server == "" || *token == "" || *ca == ""):
		if mustJoin {
			return errors.New("--server, --token and --ca are required: copy the command from the Nodes page")
		}
		return agent.ErrNotJoined
	case !joined:
		cfg, err := agent.Join(ctx, agent.JoinOptions{
			Server: *server, Token: *token, CAFingerprint: *ca, StateDir: *stateDir, AgentServer: *agentServer,
		})
		if err != nil {
			return err
		}
		logger.Info("joined the server", "node", cfg.NodeName, "state_dir", *stateDir)
	}

	st, err := agent.LoadState(*stateDir)
	if err != nil {
		return err
	}
	dc, err := docker.New(*dockerHost)
	if err != nil {
		return err
	}
	if _, err := dc.Info(ctx); err != nil {
		logger.Warn("Docker is unreachable: the node cannot run apps", "host", *dockerHost, "err", err)
	}
	logger.Info("starting agent", "node", st.NodeName, "server", st.AgentServer)
	return agent.Run(ctx, st, dc, logger)
}
