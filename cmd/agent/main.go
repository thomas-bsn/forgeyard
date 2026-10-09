// Command agent runs on each node and executes the control plane's orders on the local Docker daemon.
//
//	forgeyard-agent join --server URL --token TOKEN --ca sha256:…   join once, then run
//	forgeyard-agent run                                             run (e.g. as a service)
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

const defaultStateDir = "forgeyard-agent-data"

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
		err = join(ctx, os.Args[2:], logger)
	case "run":
		err = run(ctx, os.Args[2:], logger)
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "erreur :", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage : forgeyard-agent join --server URL --token TOKEN --ca FINGERPRINT | forgeyard-agent run")
	os.Exit(2)
}

func join(ctx context.Context, args []string, logger *slog.Logger) error {
	fs := flag.NewFlagSet("join", flag.ExitOnError)
	server := fs.String("server", "", "URL of the Forgeyard server")
	token := fs.String("token", "", "one-time join token")
	ca := fs.String("ca", "", "fingerprint of the server's CA (sha256:…)")
	stateDir := fs.String("state-dir", defaultStateDir, "where the node identity is stored")
	dockerHost := fs.String("docker-host", docker.DefaultHost(), "Docker daemon socket")
	fs.Parse(args)
	if *server == "" || *token == "" || *ca == "" {
		return errors.New("--server, --token and --ca are required: copy the command from the Nodes page")
	}

	cfg, err := agent.Join(ctx, agent.JoinOptions{Server: *server, Token: *token, CAFingerprint: *ca, StateDir: *stateDir})
	if err != nil {
		return err
	}
	logger.Info("joined the server", "node", cfg.NodeName, "state_dir", *stateDir)
	return runWith(ctx, *stateDir, *dockerHost, logger)
}

func run(ctx context.Context, args []string, logger *slog.Logger) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	stateDir := fs.String("state-dir", defaultStateDir, "where the node identity is stored")
	dockerHost := fs.String("docker-host", docker.DefaultHost(), "Docker daemon socket")
	fs.Parse(args)
	return runWith(ctx, *stateDir, *dockerHost, logger)
}

func runWith(ctx context.Context, stateDir, dockerHost string, logger *slog.Logger) error {
	st, err := agent.LoadState(stateDir)
	if err != nil {
		return err
	}
	dc, err := docker.New(dockerHost)
	if err != nil {
		return err
	}
	if _, err := dc.Info(ctx); err != nil {
		logger.Warn("Docker is unreachable: the node will report no containers", "host", dockerHost, "err", err)
	}
	logger.Info("starting agent", "node", st.NodeName, "server", st.AgentServer)
	return agent.Run(ctx, st, dc, logger)
}
