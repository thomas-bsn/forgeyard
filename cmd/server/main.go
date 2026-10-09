// Command server runs the Forgeyard control plane: web UI, API and node orchestration.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/api"
	"github.com/thomas-bsn/forgeyard/internal/auth"
	"github.com/thomas-bsn/forgeyard/internal/secrets"
	"github.com/thomas-bsn/forgeyard/internal/store"
	"github.com/thomas-bsn/forgeyard/web"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	dataDir := flag.String("data-dir", "data", "directory holding the database")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(*addr, *dataDir, logger); err != nil {
		logger.Error("server failed", "err", err)
		os.Exit(1)
	}
}

func run(addr, dataDir string, logger *slog.Logger) error {
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
		Addr:              addr,
		Handler:           api.NewServer(st, logger, box, setupToken).Handler(web.Dist()),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go cleanExpiredSessions(ctx, st, logger)

	errc := make(chan error, 1)
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
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func cleanExpiredSessions(ctx context.Context, st *store.Store, logger *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if err := st.DeleteExpiredSessions(ctx, time.Now().Unix()); err != nil && ctx.Err() == nil {
			logger.Warn("cleaning expired sessions failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
