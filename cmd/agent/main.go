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
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agent"
	"github.com/thomas-bsn/forgeyard/internal/docker"
	"github.com/thomas-bsn/forgeyard/internal/version"
)

func main() {
	// The agent's log goes to its output and to agent.Logs, which the Nodes page shows.
	logger := slog.New(slog.NewTextHandler(io.MultiWriter(os.Stdout, agent.Logs), nil))
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
	case "remove-self":
		// Run by the agent in a short-lived container when its node is removed: deletes the agent's container
		// and identity volume.
		if len(os.Args) != 4 {
			usage()
		}
		var dc *docker.Client
		if dc, err = docker.New(docker.DefaultHost()); err == nil {
			time.Sleep(2 * time.Second) // the agent's last message goes out
			err = agent.RemoveSelf(ctx, dc, os.Args[2], os.Args[3])
		}
	case "version":
		fmt.Println(version.Short())
	case "replace":
		// Run by the agent in a short-lived container of its next version: replaces the agent's container.
		if len(os.Args) != 4 {
			usage()
		}
		var dc *docker.Client
		if dc, err = docker.New(docker.DefaultHost()); err == nil {
			err = agent.Replace(ctx, dc, os.Args[2], os.Args[3])
		}
	case "host-ip":
		// Run by the agent in a short-lived container on the host's network: prints the host's local IP.
		fmt.Println(agent.RouteIP())
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "erreur :", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage : forgeyard-agent join|run|version [--server URL --token TOKEN --ca FINGERPRINT]")
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
	agentServer := fs.String("agent-server", os.Getenv("FORGEYARD_AGENT_SERVER"), "host:port of the agent port, when it differs from the server's public address (e.g. 192.168.1.10:8081 on the same local network); also changes it for a node that already joined")
	stateDir := fs.String("state-dir", defaultStateDir(), "where the node identity is stored")
	joinFile := fs.String("join-file", os.Getenv("FORGEYARD_JOIN_FILE"), "wait for the server of the same Compose project to drop join details in this file")
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
		if *joinFile == "" {
			return agent.ErrNotJoined
		}
		probeDocker, _ := docker.New(*dockerHost)
		agent.WriteHostProbe(ctx, *joinFile, probeDocker, logger)
		cfg, err := agent.WaitAndJoin(ctx, *joinFile, *stateDir, logger)
		if errors.Is(err, context.Canceled) {
			return nil
		}
		if err != nil {
			return err
		}
		logger.Info("joined the server", "node", cfg.NodeName, "state_dir", *stateDir)
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
	// The address to dial can change after joining: e.g. the public one is behind a proxy that only
	// carries HTTPS, and the node is on the server's local network.
	if *agentServer != "" && *agentServer != st.AgentServer {
		logger.Info("using another address for the server", "joined_with", st.AgentServer, "now", *agentServer)
		st.AgentServer = *agentServer
		if err := agent.SaveAgentServer(*stateDir, *agentServer); err != nil {
			logger.Warn("remembering the server's address failed: give --agent-server at each start", "err", err)
		}
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
