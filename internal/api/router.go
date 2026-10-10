// Package api exposes the control plane's HTTP API and serves the web UI.
package api

import (
	"context"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/auth"
	"github.com/thomas-bsn/forgeyard/internal/discord"
	"github.com/thomas-bsn/forgeyard/internal/dns"
	"github.com/thomas-bsn/forgeyard/internal/nodes"
	"github.com/thomas-bsn/forgeyard/internal/pki"
	"github.com/thomas-bsn/forgeyard/internal/secrets"
	"github.com/thomas-bsn/forgeyard/internal/store"
)

// Deps are the services the HTTP handlers use.
type Deps struct {
	Store   *store.Store
	Logger  *slog.Logger
	Secrets *secrets.Box
	CA      *pki.CA
	Nodes   *nodes.Hub
	// AgentPort is the port agents dial for their gRPC stream.
	AgentPort string
	// TrustedProxies may set X-Forwarded-For and X-Forwarded-Proto.
	TrustedProxies []netip.Prefix
	// AgentImage is the agent's container image shown in join commands.
	AgentImage string
	// JoinDir is shared with the agent of the Compose project, to enable this machine as a node; empty
	// when there is no such agent (e.g. the server runs as a plain binary).
	JoinDir string
	// LocalServerURL is how that agent reaches the server, e.g. http://forgeyard:8080.
	LocalServerURL string
}

// Server holds the dependencies of the HTTP handlers.
type Server struct {
	store     *store.Store
	logger    *slog.Logger
	limiter   *auth.LoginLimiter
	discord   *discord.Client
	secrets   *secrets.Box
	ca        *pki.CA
	nodes     *nodes.Hub
	agentPort string

	trustedProxies []netip.Prefix
	agentImage     string
	joinDir        string
	localServerURL string

	// Replaced in tests.
	newDNSProvider func(name string, creds map[string]string) (dns.Provider, error)
	lookupHost     func(ctx context.Context, host string) ([]string, error)
	httpClient     *http.Client // for Docker Hub logos

	mu          sync.Mutex
	setupToken  string                // empty once setup is completed
	oauthStates map[string]oauthState // pending Discord sign-ins, by state value

	crashes       map[int64][]time.Time // recent crashes, by app ID
	crashNotified map[int64]time.Time   // when the owner last heard of a crash, by app ID
	nodeGen       map[int64]int         // bumped at each connection change, to cancel a pending offline alert
	nodeReported  map[int64]bool        // nodes reported offline to the admins
}

// NewServer returns a Server. setupToken must be non-empty while the setup wizard has not been completed.
func NewServer(d Deps, setupToken string) *Server {
	s := &Server{
		store:     d.Store,
		logger:    d.Logger,
		ca:        d.CA,
		nodes:     d.Nodes,
		agentPort: d.AgentPort,

		trustedProxies: d.TrustedProxies,
		agentImage:     d.AgentImage,
		joinDir:        d.JoinDir,
		localServerURL: d.LocalServerURL,
		limiter:        auth.NewLoginLimiter(10, 15*time.Minute),
		discord:        discord.NewClient(),
		secrets:        d.Secrets,
		setupToken:     setupToken,
		oauthStates:    make(map[string]oauthState),
		crashes:        make(map[int64][]time.Time),
		crashNotified:  make(map[int64]time.Time),
		nodeGen:        make(map[int64]int),
		nodeReported:   make(map[int64]bool),

		newDNSProvider: dns.New,
		lookupHost:     net.DefaultResolver.LookupHost,
		httpClient:     &http.Client{Timeout: 10 * time.Second},
	}
	d.Nodes.Desired = s.desiredState
	d.Nodes.StateChanged = s.onAppStateChange
	d.Nodes.ExternalChanged = s.onExternalChange
	d.Nodes.NodeChanged = s.onNodeChange
	return s
}

// Handler returns the HTTP handler for the API and the single-page web UI.
func (s *Server) Handler(webFS fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/instance", s.handleInstance)
	mux.HandleFunc("GET /api/instance/icon", s.handleGetIcon)
	mux.HandleFunc("PUT /api/admin/settings/icon", s.requireSuperadmin(s.handlePutIcon))
	mux.HandleFunc("DELETE /api/admin/settings/icon", s.requireSuperadmin(s.handleDeleteIcon))
	mux.HandleFunc("POST /api/setup", s.handleSetup)
	mux.HandleFunc("POST /api/setup/discord", s.handleSetupDiscord)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/auth/me", s.requireUser(s.handleMe))
	mux.HandleFunc("PUT /api/me/profile", s.requireUser(s.handlePutProfile))
	mux.HandleFunc("PUT /api/me/avatar", s.requireUser(s.handlePutAvatar))
	mux.HandleFunc("DELETE /api/me/avatar", s.requireUser(s.handleDeleteAvatar))
	mux.HandleFunc("PUT /api/me/password", s.requireUser(s.handlePutPassword))
	mux.HandleFunc("GET /api/me/sessions", s.requireUser(s.handleListSessions))
	mux.HandleFunc("DELETE /api/me/sessions/{id}", s.requireUser(s.handleDeleteSession))
	mux.HandleFunc("GET /api/users/{id}/avatar", s.requireUser(s.handleGetAvatar))
	mux.HandleFunc("PUT /api/me/banner", s.requireUser(s.handlePutBanner))
	mux.HandleFunc("DELETE /api/me/banner", s.requireUser(s.handleDeleteBanner))
	mux.HandleFunc("GET /api/users/{id}/banner", s.requireUser(s.handleGetBanner))
	mux.HandleFunc("GET /api/members", s.requireUser(s.handleListMembers))
	mux.HandleFunc("GET /api/members/{id}", s.requireUser(s.handleGetMember))
	mux.HandleFunc("PUT /api/apps/{id}/public", s.requireUser(s.handleSetAppPublic))
	mux.HandleFunc("POST /api/admin/apps/{id}/move", s.requireAdmin(s.handleMoveApp))
	mux.HandleFunc("PUT /api/apps/{id}/logo", s.requireUser(s.handlePutAppLogo))
	mux.HandleFunc("GET /api/apps/{id}/logo", s.requireUser(s.handleGetAppLogo))
	mux.HandleFunc("GET /api/logos", s.requireUser(s.handleImageLogo))
	mux.HandleFunc("GET /api/auth/link", s.handleLoginLink)
	mux.HandleFunc("GET /api/auth/discord", s.handleDiscordStart)
	mux.HandleFunc("GET /api/auth/discord/callback", s.handleDiscordCallback)

	mux.HandleFunc("GET /api/admin/requests", s.requireAdmin(s.handleListRequests))
	mux.HandleFunc("POST /api/admin/requests/{id}/accept", s.requireAdmin(s.handleAcceptRequest))
	mux.HandleFunc("POST /api/admin/requests/{id}/refuse", s.requireAdmin(s.handleRefuseRequest))
	mux.HandleFunc("GET /api/admin/settings/discord", s.requireAdmin(s.handleGetDiscordSettings))
	mux.HandleFunc("PUT /api/admin/settings/discord", s.requireAdmin(s.handlePutDiscordSettings))
	mux.HandleFunc("GET /api/caddy/ask", s.handleCaddyAsk)
	mux.HandleFunc("GET /api/apps", s.requireUser(s.handleListApps))
	mux.HandleFunc("GET /api/admin/nodes/choices", s.requireAdmin(s.handleNodeChoices))
	mux.HandleFunc("POST /api/apps", s.requireUser(s.handleCreateApp))
	mux.HandleFunc("GET /api/apps/{id}", s.requireUser(s.handleGetApp))
	mux.HandleFunc("PUT /api/apps/{id}", s.requireUser(s.handleUpdateApp))
	mux.HandleFunc("POST /api/apps/{id}/{action}", s.requireUser(s.handleAppAction))
	mux.HandleFunc("DELETE /api/apps/{id}", s.requireUser(s.handleDeleteApp))
	mux.HandleFunc("GET /api/apps/{id}/logs", s.requireUser(s.handleAppLogs))
	mux.HandleFunc("GET /api/apps/{id}/events", s.requireUser(s.handleAppEvents))
	mux.HandleFunc("GET /api/apps/{id}/terminal", s.requireUser(s.handleAppTerminal))
	mux.HandleFunc("GET /api/admin/nodes/{node}/containers/{container}/terminal", s.requireSuperadmin(s.handleContainerTerminal))
	mux.HandleFunc("GET /api/apps/{id}/usage", s.requireUser(s.handleAppUsage))
	mux.HandleFunc("GET /api/admin/containers", s.requireAdmin(s.handleListContainers))
	mux.HandleFunc("POST /api/admin/nodes/{node}/containers/{container}/{action}", s.requireSuperadmin(s.handleContainerAction))
	mux.HandleFunc("GET /api/admin/nodes/{node}/containers/{container}/logs", s.requireSuperadmin(s.handleContainerLogs))
	mux.HandleFunc("GET /api/admin/nodes/{node}/containers/{container}/usage", s.requireAdmin(s.handleContainerUsage))
	mux.HandleFunc("GET /api/admin/nodes/{node}/containers/{container}/events", s.requireAdmin(s.handleContainerEvents))
	mux.HandleFunc("GET /api/admin/settings/notifications", s.requireAdmin(s.handleGetNotifySettings))
	mux.HandleFunc("PUT /api/admin/settings/notifications", s.requireAdmin(s.handlePutNotifySettings))
	mux.HandleFunc("POST /api/admin/settings/notifications/test", s.requireAdmin(s.handleTestAdminNotify))
	mux.HandleFunc("PUT /api/me/notifications", s.requireUser(s.handlePutMyWebhook))
	mux.HandleFunc("POST /api/me/notifications/test", s.requireUser(s.handleTestMyWebhook))
	mux.HandleFunc("GET /api/admin/users", s.requireAdmin(s.handleListUsers))
	mux.HandleFunc("PUT /api/admin/users/{id}", s.requireAdmin(s.handleUpdateUser))
	mux.HandleFunc("DELETE /api/admin/users/{id}", s.requireAdmin(s.handleDeleteUser))
	mux.HandleFunc("PUT /api/admin/settings/general", s.requireSuperadmin(s.handlePutGeneralSettings))
	mux.HandleFunc("PUT /api/admin/nodes/{id}/ingress", s.requireAdmin(s.handleNodeIngress))
	mux.HandleFunc("GET /api/admin/nodes", s.requireAdmin(s.handleListNodes))
	mux.HandleFunc("POST /api/admin/nodes", s.requireAdmin(s.handleCreateNode))
	mux.HandleFunc("POST /api/admin/nodes/{id}/join-command", s.requireAdmin(s.handleNewJoinCommand))
	mux.HandleFunc("DELETE /api/admin/nodes/{id}", s.requireAdmin(s.handleDeleteNode))
	mux.HandleFunc("GET /api/nodes/ca", s.handleCACertificate)
	mux.HandleFunc("POST /api/nodes/join", s.handleJoinNode)
	mux.HandleFunc("GET /api/admin/settings/domain", s.requireSuperadmin(s.handleGetDomainSettings))
	mux.HandleFunc("PUT /api/admin/settings/domain", s.requireSuperadmin(s.handlePutDomainSettings))
	mux.HandleFunc("POST /api/admin/settings/domain/check", s.requireSuperadmin(s.handleCheckDomain))
	mux.HandleFunc("GET /api/admin/settings/login", s.requireSuperadmin(s.handleGetLoginSettings))
	mux.HandleFunc("PUT /api/admin/settings/login", s.requireSuperadmin(s.handlePutLoginSettings))
	mux.Handle("/", spaHandler(webFS))
	return s.realClient(requireJSONForWrites(mux))
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
