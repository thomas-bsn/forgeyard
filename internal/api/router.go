// Package api exposes the control plane's HTTP API and serves the web UI.
package api

import (
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/auth"
	"github.com/thomas-bsn/forgeyard/internal/discord"
	"github.com/thomas-bsn/forgeyard/internal/secrets"
	"github.com/thomas-bsn/forgeyard/internal/store"
)

// Server holds the dependencies of the HTTP handlers.
type Server struct {
	store   *store.Store
	logger  *slog.Logger
	limiter *auth.LoginLimiter
	discord *discord.Client
	secrets *secrets.Box

	mu          sync.Mutex
	setupToken  string                // empty once setup is completed
	oauthStates map[string]oauthState // pending Discord sign-ins, by state value
}

// NewServer returns a Server. setupToken must be non-empty while the setup wizard has not been completed.
func NewServer(st *store.Store, logger *slog.Logger, box *secrets.Box, setupToken string) *Server {
	return &Server{
		store:       st,
		logger:      logger,
		limiter:     auth.NewLoginLimiter(10, 15*time.Minute),
		discord:     discord.NewClient(),
		secrets:     box,
		setupToken:  setupToken,
		oauthStates: make(map[string]oauthState),
	}
}

// Handler returns the HTTP handler for the API and the single-page web UI.
func (s *Server) Handler(webFS fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/instance", s.handleInstance)
	mux.HandleFunc("POST /api/setup", s.handleSetup)
	mux.HandleFunc("POST /api/setup/discord", s.handleSetupDiscord)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/auth/me", s.requireUser(s.handleMe))
	mux.HandleFunc("GET /api/auth/link", s.handleLoginLink)
	mux.HandleFunc("GET /api/auth/discord", s.handleDiscordStart)
	mux.HandleFunc("GET /api/auth/discord/callback", s.handleDiscordCallback)

	mux.HandleFunc("GET /api/admin/requests", s.requireAdmin(s.handleListRequests))
	mux.HandleFunc("POST /api/admin/requests/{id}/accept", s.requireAdmin(s.handleAcceptRequest))
	mux.HandleFunc("POST /api/admin/requests/{id}/refuse", s.requireAdmin(s.handleRefuseRequest))
	mux.HandleFunc("GET /api/admin/settings/discord", s.requireAdmin(s.handleGetDiscordSettings))
	mux.HandleFunc("PUT /api/admin/settings/discord", s.requireAdmin(s.handlePutDiscordSettings))
	mux.Handle("/", spaHandler(webFS))
	return requireJSONForWrites(mux)
}

// spaHandler serves static files and falls back to index.html so client-side routes work.
func spaHandler(webFS fs.FS) http.Handler {
	files := http.FileServerFS(webFS)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" {
			if _, err := fs.Stat(webFS, path); err == nil {
				files.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(webFS, "index.html")
		if err != nil {
			http.Error(w, "web UI not built: run `make web`", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(index)
	})
}
