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
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

const (
	settingSetupCompleted = "setup_completed"
	settingInstanceName   = "instance_name"
	defaultInstanceName   = "Forgeyard"
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
	DiscordEnabled     bool   `json:"discordEnabled"`
	DiscordRedirectURL string `json:"discordRedirectUrl"`
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
	writeJSON(w, http.StatusOK, instanceResponse{
		Name:               name,
		SetupRequired:      s.setupRequired(),
		DiscordEnabled:     enabled && !s.setupRequired(),
		DiscordRedirectURL: redirectURL,
	})
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
	Username     string `json:"username"`
	Password     string `json:"password"`
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
		if err := q.SetSetting(r.Context(), db.SetSettingParams{Key: settingPublicURL, Value: requestOrigin(r)}); err != nil {
			return err
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
	s.logger.Info("setup completed", "superadmin", user.Username.String)
	writeJSON(w, http.StatusCreated, toUserResponse(user))
}
