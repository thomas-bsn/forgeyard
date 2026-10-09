package api

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/thomas-bsn/forgeyard/internal/auth"
	"github.com/thomas-bsn/forgeyard/internal/dns"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

const (
	settingSetupCompleted = "setup_completed"
	settingInstanceName   = "instance_name"
	// settingPasswordLogin is "0" when signing in with a username and password is turned off.
	// It is off after a Discord setup and on after a password setup; only the superadmin changes it.
	settingPasswordLogin = "password_login"
	defaultInstanceName  = "Forgeyard"
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9_-]{3,32}$`)

var errSetupDone = errors.New("setup already completed")

// SetupCompleted reports whether the setup wizard has been completed.
func SetupCompleted(ctx context.Context, q *db.Queries) (bool, error) {
	_, err := q.GetSetting(ctx, settingSetupCompleted)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *Server) setupRequired() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.setupToken != ""
}

type instanceResponse struct {
	Name               string `json:"name"`
	SetupRequired      bool   `json:"setupRequired"`
	PasswordLogin      bool   `json:"passwordLoginEnabled"`
	LocalNodeSupported bool   `json:"localNodeSupported"`
	// LocalWebPorts tells, during setup, whether this machine's ports 80 and 443 are already taken.
	LocalWebPorts string `json:"localWebPorts,omitempty"`
	// DNSProviders are listed during setup only, for the wizard's domain step.
	DNSProviders       []dns.Kind `json:"dnsProviders,omitempty"`
	DiscordEnabled     bool       `json:"discordEnabled"`
	DiscordRedirectURL string     `json:"discordRedirectUrl"`
}

func (s *Server) handleInstance(w http.ResponseWriter, r *http.Request) {
	name, err := s.store.GetSetting(r.Context(), settingInstanceName)
	if errors.Is(err, sql.ErrNoRows) {
		name = defaultInstanceName
	} else if err != nil {
		s.internalError(w, r, err)
		return
	}
	enabled, redirectURL, err := s.discordInfo(r.Context(), r)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	passwordLogin, err := passwordLoginEnabled(r.Context(), s.store.Queries)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	var providers []dns.Kind
	var webPorts string
	if s.setupRequired() {
		providers = dns.Kinds
		webPorts = s.localWebPorts()
	}
	writeJSON(w, http.StatusOK, instanceResponse{
		DNSProviders:       providers,
		Name:               name,
		SetupRequired:      s.setupRequired(),
		PasswordLogin:      passwordLogin || s.setupRequired(),
		LocalNodeSupported: s.localNodeSupported(),
		LocalWebPorts:      webPorts,
		DiscordEnabled:     enabled && !s.setupRequired(),
		DiscordRedirectURL: redirectURL,
	})
}

// passwordLoginEnabled reports whether username and password sign-in is allowed (true when never set).
func passwordLoginEnabled(ctx context.Context, q *db.Queries) (bool, error) {
	v, err := q.GetSetting(ctx, settingPasswordLogin)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return v != "0", err
}

// checkSetupToken writes an error and returns false unless token is the current setup token.
func (s *Server) checkSetupToken(w http.ResponseWriter, token string) bool {
	s.mu.Lock()
	expected := s.setupToken
	s.mu.Unlock()
	if expected == "" {
		writeError(w, http.StatusConflict, "l'installation est déjà terminée")
		return false
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(token)), []byte(expected)) != 1 {
		writeError(w, http.StatusForbidden, "token de setup invalide")
		return false
	}
	return true
}

// setupPublicURL validates the address chosen in the wizard, defaulting to the one the browser uses.
func setupPublicURL(w http.ResponseWriter, r *http.Request, raw string) (string, bool) {
	if strings.TrimSpace(raw) == "" {
		return requestOrigin(r), true
	}
	u, ok := normalizePublicURL(raw)
	if !ok {
		writeError(w, http.StatusBadRequest, "adresse de Forgeyard invalide : par exemple https://forgeyard.mondomaine.com")
	}
	return u, ok
}

// validInstanceName trims the name and writes an error unless it is 1 to 64 characters long.
func validInstanceName(w http.ResponseWriter, name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 64 {
		writeError(w, http.StatusBadRequest, "le nom doit faire entre 1 et 64 caractères")
		return "", false
	}
	return name, true
}

type setupRequest struct {
	Token        string `json:"token"`
	InstanceName string `json:"instanceName"`
	PublicURL    string `json:"publicUrl"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	LocalNode    bool   `json:"localNode"`
	// Domain and Ingress are optional: without them, apps get no domain and the local node uses Traefik.
	Domain  *domainChoice `json:"domain"`
	Ingress setupIngress  `json:"ingress"`
}

// prepareSetupDomain validates the domain and ingress chosen in the wizard, checking DNS credentials.
func (s *Server) prepareSetupDomain(ctx context.Context, w http.ResponseWriter, r *http.Request, d *domainChoice, in setupIngress) (map[string]string, setupIngress, bool) {
	ingress, err := in.validate()
	var bad badRequest
	if errors.As(err, &bad) {
		writeError(w, http.StatusBadRequest, bad.msg)
		return nil, ingress, false
	}
	values := map[string]string{}
	if d != nil {
		values, err = s.prepareDomain(ctx, *d)
		if errors.As(err, &bad) {
			writeError(w, http.StatusBadRequest, bad.msg)
			return nil, ingress, false
		}
		if err != nil {
			s.internalError(w, r, err)
			return nil, ingress, false
		}
	}
	return values, ingress, true
}

// handleSetup completes the first-run wizard: it creates the superadmin and logs them in.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	var req setupRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if !s.checkSetupToken(w, req.Token) {
		return
	}
	name, ok := validInstanceName(w, req.InstanceName)
	if !ok {
		return
	}
	publicURL, ok := setupPublicURL(w, r, req.PublicURL)
	if !ok {
		return
	}
	domainValues, ingress, ok := s.prepareSetupDomain(r.Context(), w, r, req.Domain, req.Ingress)
	if !ok {
		return
	}
	req.Username = strings.ToLower(strings.TrimSpace(req.Username))
	switch {
	case !usernamePattern.MatchString(req.Username):
		writeError(w, http.StatusBadRequest, "identifiant : 3 à 32 caractères parmi a-z, 0-9, _ et -")
		return
	case utf8.RuneCountInString(req.Password) < auth.MinPasswordLength:
		writeError(w, http.StatusBadRequest, "le mot de passe doit faire au moins 12 caractères")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	var user db.User
	var joinToken string
	err = s.store.InTx(r.Context(), func(q *db.Queries) error {
		done, err := SetupCompleted(r.Context(), q)
		if err != nil {
			return err
		}
		if done {
			return errSetupDone
		}
		user, err = q.CreateLocalUser(r.Context(), db.CreateLocalUserParams{
			Username:     sql.NullString{String: req.Username, Valid: true},
			PasswordHash: sql.NullString{String: hash, Valid: true},
			DisplayName:  req.Username,
			Role:         "superadmin",
			CreatedAt:    time.Now().Unix(),
		})
		if err != nil {
			return err
		}
		if err := q.SetSetting(r.Context(), db.SetSettingParams{Key: settingInstanceName, Value: name}); err != nil {
			return err
		}
		if err := q.SetSetting(r.Context(), db.SetSettingParams{Key: settingPublicURL, Value: publicURL}); err != nil {
			return err
		}
		if err := q.SetSetting(r.Context(), db.SetSettingParams{Key: settingPasswordLogin, Value: "1"}); err != nil {
			return err
		}
		if err := storeSettings(r.Context(), q, domainValues); err != nil {
			return err
		}
		if req.LocalNode && s.localNodeSupported() {
			if joinToken, err = s.createLocalNode(r.Context(), q, ingress); err != nil {
				return err
			}
		}
		if err := q.SetSetting(r.Context(), db.SetSettingParams{Key: settingSetupCompleted, Value: "1"}); err != nil {
			return err
		}
		return auth.CreateSession(r.Context(), q, w, r, user.ID)
	})
	if errors.Is(err, errSetupDone) {
		writeError(w, http.StatusConflict, "l'installation est déjà terminée")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	s.mu.Lock()
	s.setupToken = ""
	s.mu.Unlock()
	if joinToken != "" {
		if err := s.writeLocalJoin(joinToken); err != nil {
			s.logger.Error("enabling this machine as a node failed", "err", err)
		}
	}
	s.logger.Info("setup completed", "superadmin", user.Username.String, "local_node", joinToken != "")
	writeJSON(w, http.StatusCreated, toUserResponse(user))
}
