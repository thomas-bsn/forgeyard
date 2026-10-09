// Command server runs the Forgeyard control plane: web UI, API and node orchestration.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/api"
	"github.com/thomas-bsn/forgeyard/internal/auth"
	"github.com/thomas-bsn/forgeyard/internal/nodes"
	"github.com/thomas-bsn/forgeyard/internal/pki"
	"github.com/thomas-bsn/forgeyard/internal/secrets"
	"github.com/thomas-bsn/forgeyard/internal/store"
	"github.com/thomas-bsn/forgeyard/web"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "admin-login" {
		if err := adminLogin(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "erreur :", err)
			os.Exit(1)
		}
		return
	}

	addr := flag.String("addr", ":8080", "HTTP listen address")
	agentAddr := flag.String("agent-addr", ":8081", "listen address of the agents' gRPC port (mutual TLS)")
	dataDir := flag.String("data-dir", "data", "directory holding the database")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(*addr, *agentAddr, *dataDir, logger); err != nil {
		logger.Error("server failed", "err", err)
		os.Exit(1)
	}
}

// adminLogin prints a one-time link that signs in as the superadmin, for when the UI is out of reach
// (Discord down or misconfigured, password forgotten). Running it requires access to the server.
func adminLogin(args []string) error {
	fs := flag.NewFlagSet("admin-login", flag.ExitOnError)
	dataDir := fs.String("data-dir", "data", "directory holding the database")
	fs.Parse(args)

	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(*dataDir, "forgeyard.db"))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()

	link, user, err := api.CreateSuperadminLoginLink(ctx, st)
	if err != nil {
		return err
	}
	fmt.Printf("\n  Lien de connexion pour %s (superadmin), valable %d minutes et utilisable une seule fois :\n\n    %s\n\n",
		user.DisplayName, int(api.LoginLinkTTL.Minutes()), link)
	return nil
}

func run(addr, agentAddr, dataDir string, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	st, err := store.Open(ctx, filepath.Join(dataDir, "forgeyard.db"))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()

	box, err := secrets.LoadOrCreate(dataDir)
	if err != nil {
		return fmt.Errorf("load secret key: %w", err)
	}
	ca, err := pki.LoadOrCreateCA(dataDir)
	if err != nil {
		return fmt.Errorf("load certificate authority: %w", err)
	}
	hub := nodes.NewHub(st, logger)
	trustedProxies := api.DefaultTrustedProxies
	if v, ok := os.LookupEnv("FORGEYARD_TRUSTED_PROXIES"); ok {
		if trustedProxies, err = api.ParseTrustedProxies(v); err != nil {
			return err
		}
	}
	_, agentPort, err := net.SplitHostPort(agentAddr)
	if err != nil {
		return fmt.Errorf("agent-addr: %w", err)
	}

	done, err := api.SetupCompleted(ctx, st.Queries)
	if err != nil {
		return err
	}
	var setupToken string
	if !done {
		// The token only lives in memory: a restart prints a new one.
		setupToken = auth.NewToken()
		fmt.Printf("\n  Forgeyard n'est pas encore configuré.\n"+
			"  Ouvrez l'interface web et saisissez ce token de setup :\n\n    %s\n\n", setupToken)
	}

	srv := &http.Server{
		Addr: addr,
		Handler: api.NewServer(api.Deps{
			Store: st, Logger: logger, Secrets: box, CA: ca, Nodes: hub, AgentPort: agentPort,
			TrustedProxies: trustedProxies,
			AgentImage:     envOr("FORGEYARD_AGENT_IMAGE", "ghcr.io/thomas-bsn/forgeyard-agent:latest"),
			JoinDir:        os.Getenv("FORGEYARD_JOIN_DIR"),
			LocalServerURL: envOr("FORGEYARD_LOCAL_SERVER_URL", "http://forgeyard:8080"),
		}, setupToken).Handler(web.Dist()),
		ReadHeaderTimeout: 10 * time.Second,
	}

	tlsConfig, err := ca.ServerTLS()
	if err != nil {
		return err
	}
	grpcServer := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(tlsConfig)),
		// Detect dead agents (and keep NAT mappings open) with pings.
		grpc.KeepaliveParams(keepalive.ServerParameters{Time: 30 * time.Second, Timeout: 10 * time.Second}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 10 * time.Second, PermitWithoutStream: true}),
	)
	agentpb.RegisterAgentServiceServer(grpcServer, hub)
	agentListener, err := net.Listen("tcp", agentAddr)
	if err != nil {
		return fmt.Errorf("agent port: %w", err)
	}

	go cleanExpired(ctx, st, logger)

	errc := make(chan error, 2)
	go func() {
		logger.Info("agent port listening", "addr", agentAddr)
		if err := grpcServer.Serve(agentListener); err != nil {
			errc <- err
		}
	}()
	go func() {
		logger.Info("forgeyard server listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	logger.Info("shutting down")
	// Agent streams never end on their own: drop them instead of waiting.
	grpcServer.Stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func cleanExpired(ctx context.Context, st *store.Store, logger *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		now := time.Now().Unix()
		if err := st.DeleteExpiredSessions(ctx, now); err != nil && ctx.Err() == nil {
			logger.Warn("cleaning expired sessions failed", "err", err)
		}
		if err := st.DeleteExpiredLoginLinks(ctx, now); err != nil && ctx.Err() == nil {
			logger.Warn("cleaning expired login links failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
