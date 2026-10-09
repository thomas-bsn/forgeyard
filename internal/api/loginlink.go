package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/auth"
	"github.com/thomas-bsn/forgeyard/internal/store"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

// LoginLinkTTL is how long a recovery link stays valid.
const LoginLinkTTL = 15 * time.Minute

const loginLinkPath = "/api/auth/link"

// CreateSuperadminLoginLink stores a one-time sign-in link for the superadmin and returns its URL.
// It is meant for the `admin-login` command, run on the server by someone locked out of the UI.
func CreateSuperadminLoginLink(ctx context.Context, st *store.Store) (string, db.User, error) {
	done, err := SetupCompleted(ctx, st.Queries)
	if err != nil {
		return "", db.User{}, err
	}
	if !done {
		return "", db.User{}, errors.New("l'installation n'est pas terminée : ouvrez l'interface web pour faire le wizard")
	}
	user, err := st.GetSuperadmin(ctx)
	if err != nil {
		return "", db.User{}, fmt.Errorf("superadmin introuvable: %w", err)
	}
	base, err := st.GetSetting(ctx, settingPublicURL)
	if errors.Is(err, sql.ErrNoRows) {
		base = "http://localhost:8080"
	} else if err != nil {
		return "", db.User{}, err
	}

	token := auth.NewToken()
	if err := st.CreateLoginLink(ctx, db.CreateLoginLinkParams{
		TokenHash: auth.HashToken(token),
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(LoginLinkTTL).Unix(),
	}); err != nil {
		return "", db.User{}, err
	}
	return strings.TrimRight(base, "/") + loginLinkPath + "?token=" + url.QueryEscape(token), user, nil
}

// handleLoginLink signs in with a one-time link and sends the browser to the dashboard.
func (s *Server) handleLoginLink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID, err := s.store.ConsumeLoginLink(ctx, db.ConsumeLoginLinkParams{
		TokenHash: auth.HashToken(r.URL.Query().Get("token")),
		ExpiresAt: time.Now().Unix(),
	})
	if errors.Is(err, sql.ErrNoRows) {
		http.Redirect(w, r, "/?link=invalid", http.StatusFound)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := auth.CreateSession(ctx, s.store.Queries, w, r, user.ID); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.logger.Warn("signed in with a recovery link", "user", user.DisplayName, "ip", clientIP(r))
	http.Redirect(w, r, "/", http.StatusFound)
}
