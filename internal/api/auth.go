package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/thomas-bsn/forgeyard/internal/auth"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

type userResponse struct {
	ID          int64  `json:"id"`
	Username    string `json:"username,omitempty"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"`
}

func toUserResponse(u db.User) userResponse {
	return userResponse{ID: u.ID, Username: u.Username.String, DisplayName: u.DisplayName, Role: u.Role}
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	enabled, err := passwordLoginEnabled(r.Context(), s.store.Queries)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !enabled {
		writeError(w, http.StatusForbidden, "la connexion par identifiant est désactivée sur cette instance")
		return
	}
	ip := clientIP(r)
	if !s.limiter.Allowed(ip) {
		writeError(w, http.StatusTooManyRequests, "trop de tentatives, réessayez dans quelques minutes")
		return
	}

	user, err := s.store.GetUserByUsername(r.Context(), sql.NullString{
		String: strings.ToLower(strings.TrimSpace(req.Username)), Valid: true,
	})
	if errors.Is(err, sql.ErrNoRows) {
		auth.VerifyDummy(req.Password)
		s.limiter.Fail(ip)
		writeError(w, http.StatusUnauthorized, "identifiant ou mot de passe incorrect")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	ok, err := auth.VerifyPassword(req.Password, user.PasswordHash.String)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !ok || user.Disabled != 0 {
		s.limiter.Fail(ip)
		writeError(w, http.StatusUnauthorized, "identifiant ou mot de passe incorrect")
		return
	}

	s.limiter.Reset(ip)
	if err := auth.CreateSession(r.Context(), s.store.Queries, w, r, user.ID); err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toUserResponse(user))
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := auth.DestroySession(r.Context(), s.store.Queries, w, r); err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, toUserResponse(currentUser(r)))
}
