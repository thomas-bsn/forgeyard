package api

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/auth"
	"github.com/thomas-bsn/forgeyard/internal/discord"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

const (
	settingDiscordClientID     = "discord_client_id"
	settingDiscordClientSecret = "discord_client_secret"
	settingPublicURL           = "public_url"

	discordCallbackPath = "/api/auth/discord/callback"
	oauthStateCookie    = "forgeyard_oauth_state"
	oauthStateTTL       = 10 * time.Minute
)

// oauthState is a Discord sign-in in progress. The setup purpose makes the Discord account the superadmin;
// it is only issued to someone who presented the setup token.
type oauthState struct {
	purpose   string       // "login" or "setup"
	localNode bool         // setup: also enable this machine as a node
	ingress   setupIngress // setup: how that node receives web traffic
	expires   time.Time
}

// newOAuthState registers a state value and binds it to the browser with a cookie, so a callback
// cannot be replayed in someone else's browser (login CSRF).
func (s *Server) newOAuthState(w http.ResponseWriter, r *http.Request, st oauthState) string {
	state := auth.NewToken()
	now := time.Now()
	st.expires = now.Add(oauthStateTTL)
	s.mu.Lock()
	for k, v := range s.oauthStates {
		if now.After(v.expires) {
			delete(s.oauthStates, k)
		}
	}
	s.oauthStates[state] = st
	s.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    state,
		Path:     "/api/auth/discord",
		MaxAge:   int(oauthStateTTL.Seconds()),
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		SameSite: http.SameSiteLaxMode,
	})
	return state
}

// consumeOAuthState validates and deletes the state of a callback.
func (s *Server) consumeOAuthState(w http.ResponseWriter, r *http.Request) (oauthState, bool) {
	state := r.URL.Query().Get("state")
	cookie, err := r.Cookie(oauthStateCookie)
	http.SetCookie(w, &http.Cookie{Name: oauthStateCookie, Value: "", Path: "/api/auth/discord", MaxAge: -1})
	if err != nil || state == "" || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(state)) != 1 {
		return oauthState{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.oauthStates[state]
	delete(s.oauthStates, state)
	if !ok || time.Now().After(st.expires) {
		return oauthState{}, false
	}
	return st, true
}

// publicURL is the address the instance is reached at, recorded during setup.
func (s *Server) publicURL(ctx context.Context, r *http.Request) (string, error) {
	u, err := s.store.GetSetting(ctx, settingPublicURL)
	if errors.Is(err, sql.ErrNoRows) {
		return requestOrigin(r), nil
	}
	return u, err
}

func (s *Server) discordRedirectURL(ctx context.Context, r *http.Request) (string, error) {
	base, err := s.publicURL(ctx, r)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(base, "/") + discordCallbackPath, nil
}

// discordConfig returns the configured Discord application; ok is false when Discord sign-in is not set up.
func (s *Server) discordConfig(ctx context.Context, r *http.Request) (cfg discord.Config, ok bool, err error) {
	id, err := s.store.GetSetting(ctx, settingDiscordClientID)
	if errors.Is(err, sql.ErrNoRows) {
		return cfg, false, nil
	} else if err != nil {
		return cfg, false, err
	}
	sealed, err := s.store.GetSetting(ctx, settingDiscordClientSecret)
	if errors.Is(err, sql.ErrNoRows) {
		return cfg, false, nil
	} else if err != nil {
		return cfg, false, err
	}
	secret, err := s.secrets.Decrypt(sealed, settingDiscordClientSecret)
	if err != nil {
		return cfg, false, fmt.Errorf("discord client secret: %w", err)
	}
	redirect, err := s.discordRedirectURL(ctx, r)
	if err != nil {
		return cfg, false, err
	}
	return discord.Config{ClientID: id, ClientSecret: secret, RedirectURL: redirect}, true, nil
}

// saveDiscordConfig stores the client ID and, when not empty, the encrypted client secret.
func (s *Server) saveDiscordConfig(ctx context.Context, q *db.Queries, clientID, clientSecret string) error {
	if err := q.SetSetting(ctx, db.SetSettingParams{Key: settingDiscordClientID, Value: clientID}); err != nil {
		return err
	}
	if clientSecret == "" {
		return nil
	}
	sealed, err := s.secrets.Encrypt(clientSecret, settingDiscordClientSecret)
	if err != nil {
		return err
	}
	return q.SetSetting(ctx, db.SetSettingParams{Key: settingDiscordClientSecret, Value: sealed})
}

// handleDiscordStart sends the browser to Discord's consent page.
func (s *Server) handleDiscordStart(w http.ResponseWriter, r *http.Request) {
	cfg, ok, err := s.discordConfig(r.Context(), r)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !ok || s.setupRequired() {
		http.Redirect(w, r, "/?discord=unavailable", http.StatusFound)
		return
	}
	http.Redirect(w, r, s.discord.AuthorizeURL(cfg, s.newOAuthState(w, r, oauthState{purpose: "login"})), http.StatusFound)
}

type setupDiscordRequest struct {
	Token        string        `json:"token"`
	InstanceName string        `json:"instanceName"`
	PublicURL    string        `json:"publicUrl"`
	ClientID     string        `json:"clientId"`
	ClientSecret string        `json:"clientSecret"`
	LocalNode    bool          `json:"localNode"`
	Domain       *domainChoice `json:"domain"`
	Ingress      setupIngress  `json:"ingress"`
}

// handleSetupDiscord saves the Discord application during setup and returns the consent URL. The first
// Discord account to come back through it becomes the superadmin.
func (s *Server) handleSetupDiscord(w http.ResponseWriter, r *http.Request) {
	var req setupDiscordRequest
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
	// Discord sends the browser back to the public address, where the sign-in cookie must already be set.
	if publicURL != requestOrigin(r) {
		writeError(w, http.StatusBadRequest, "pour configurer Discord, ouvrez le wizard depuis "+publicURL+" : Discord ne renvoie que vers cette adresse")
		return
	}
	domainValues, ingress, ok := s.prepareSetupDomain(r.Context(), w, r, req.Domain, req.Ingress)
	if !ok {
		return
	}
	req.ClientID = strings.TrimSpace(req.ClientID)
	req.ClientSecret = strings.TrimSpace(req.ClientSecret)
	if req.ClientID == "" || req.ClientSecret == "" {
		writeError(w, http.StatusBadRequest, "le Client ID et le Client Secret sont obligatoires")
		return
	}

	err := s.store.InTx(r.Context(), func(q *db.Queries) error {
		if err := s.saveDiscordConfig(r.Context(), q, req.ClientID, req.ClientSecret); err != nil {
			return err
		}
		if err := q.SetSetting(r.Context(), db.SetSettingParams{Key: settingInstanceName, Value: name}); err != nil {
			return err
		}
		if err := storeSettings(r.Context(), q, domainValues); err != nil {
			return err
		}
		return q.SetSetting(r.Context(), db.SetSettingParams{Key: settingPublicURL, Value: publicURL})
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	cfg, _, err := s.discordConfig(r.Context(), r)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"authorizeUrl": s.discord.AuthorizeURL(cfg, s.newOAuthState(w, r, oauthState{purpose: "setup", localNode: req.LocalNode && s.localNodeSupported(), ingress: ingress})),
	})
}

// handleDiscordCallback finishes a Discord sign-in: it logs the user in, creates the superadmin during
// setup, or records an account request for an unknown Discord account.
func (s *Server) handleDiscordCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, ok := s.consumeOAuthState(w, r)
	if !ok {
		http.Redirect(w, r, "/?discord=expired", http.StatusFound)
		return
	}
	if r.URL.Query().Get("error") != "" {
		// The user clicked "Cancel" on Discord's consent page.
		http.Redirect(w, r, "/?discord=cancelled", http.StatusFound)
		return
	}
	cfg, configured, err := s.discordConfig(ctx, r)
	if err != nil || !configured {
		s.redirectDiscordError(w, r, err)
		return
	}
	dUser, err := s.discord.Authenticate(ctx, cfg, r.URL.Query().Get("code"))
	if err != nil {
		s.redirectDiscordError(w, r, err)
		return
	}

	if st.purpose == "setup" {
		s.completeDiscordSetup(w, r, dUser, st.localNode, st.ingress)
		return
	}

	user, err := s.store.GetUserByDiscordID(ctx, sql.NullString{String: dUser.ID, Valid: true})
	switch {
	case err == nil:
		if user.Disabled != 0 {
			http.Redirect(w, r, "/?discord=disabled", http.StatusFound)
			return
		}
		if err := s.store.UpdateDiscordProfile(ctx, db.UpdateDiscordProfileParams{
			DisplayName: dUser.DisplayName(), Email: nullString(dUser.VerifiedEmail()), ID: user.ID,
		}); err != nil {
			s.redirectDiscordError(w, r, err)
			return
		}
		if err := auth.CreateSession(ctx, s.store.Queries, w, r, user.ID); err != nil {
			s.redirectDiscordError(w, r, err)
			return
		}
		http.Redirect(w, r, "/", http.StatusFound)
	case errors.Is(err, sql.ErrNoRows):
		status, err := s.recordAccountRequest(ctx, dUser)
		if err != nil {
			s.redirectDiscordError(w, r, err)
			return
		}
		http.Redirect(w, r, "/?discord="+status, http.StatusFound)
	default:
		s.redirectDiscordError(w, r, err)
	}
}

func (s *Server) completeDiscordSetup(w http.ResponseWriter, r *http.Request, dUser discord.User, localNode bool, ingress setupIngress) {
	ctx := r.Context()
	var user db.User
	var joinToken string
	err := s.store.InTx(ctx, func(q *db.Queries) error {
		done, err := SetupCompleted(ctx, q)
		if err != nil {
			return err
		}
		if done {
			return errSetupDone
		}
		user, err = q.CreateDiscordUser(ctx, db.CreateDiscordUserParams{
			DiscordID:   nullString(dUser.ID),
			DisplayName: dUser.DisplayName(),
			Email:       nullString(dUser.VerifiedEmail()),
			Role:        "superadmin",
			CreatedAt:   time.Now().Unix(),
		})
		if err != nil {
			return err
		}
		if err := q.SetSetting(ctx, db.SetSettingParams{Key: settingPasswordLogin, Value: "0"}); err != nil {
			return err
		}
		if localNode {
			if joinToken, err = s.createLocalNode(ctx, q, ingress); err != nil {
				return err
			}
		}
		if err := q.SetSetting(ctx, db.SetSettingParams{Key: settingSetupCompleted, Value: "1"}); err != nil {
			return err
		}
		return auth.CreateSession(ctx, q, w, r, user.ID)
	})
	if errors.Is(err, errSetupDone) {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if err != nil {
		s.redirectDiscordError(w, r, err)
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
	s.logger.Info("setup completed", "superadmin", user.DisplayName, "discord_id", dUser.ID, "local_node", localNode)
	http.Redirect(w, r, "/", http.StatusFound)
}

// recordAccountRequest creates or refreshes the request of an unknown Discord account and returns its status.
func (s *Server) recordAccountRequest(ctx context.Context, dUser discord.User) (string, error) {
	existing, err := s.store.GetAccountRequestByDiscordID(ctx, dUser.ID)
	if err == nil {
		err = s.store.UpdateAccountRequestProfile(ctx, db.UpdateAccountRequestProfileParams{
			Username: dUser.Username, DisplayName: dUser.DisplayName(),
			Email: nullString(dUser.VerifiedEmail()), Avatar: nullString(dUser.Avatar), ID: existing.ID,
		})
		return existing.Status, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	_, err = s.store.CreateAccountRequest(ctx, db.CreateAccountRequestParams{
		DiscordID: dUser.ID, Username: dUser.Username, DisplayName: dUser.DisplayName(),
		Email: nullString(dUser.VerifiedEmail()), Avatar: nullString(dUser.Avatar), CreatedAt: time.Now().Unix(),
	})
	if err != nil {
		return "", err
	}
	s.logger.Info("new account request", "discord_user", dUser.Username)
	return "pending", nil
}

func (s *Server) redirectDiscordError(w http.ResponseWriter, r *http.Request, err error) {
	if err != nil {
		s.logger.Error("discord sign-in failed", "err", err)
	}
	http.Redirect(w, r, "/?discord=error", http.StatusFound)
}

func nullString(v string) sql.NullString {
	return sql.NullString{String: v, Valid: v != ""}
}

// discordInfo is what the UI needs to show the Discord button and the setup instructions.
func (s *Server) discordInfo(ctx context.Context, r *http.Request) (enabled bool, redirectURL string, err error) {
	_, enabled, err = s.discordConfig(ctx, r)
	if err != nil {
		return false, "", err
	}
	redirectURL, err = s.discordRedirectURL(ctx, r)
	return enabled, redirectURL, err
}
