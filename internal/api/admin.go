package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

type accountRequestResponse struct {
	ID          int64  `json:"id"`
	DiscordID   string `json:"discordId"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email,omitempty"`
	AvatarURL   string `json:"avatarUrl,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
}

func toAccountRequestResponse(a db.AccountRequest) accountRequestResponse {
	resp := accountRequestResponse{
		ID: a.ID, DiscordID: a.DiscordID, Username: a.Username, DisplayName: a.DisplayName,
		Email: a.Email.String, CreatedAt: a.CreatedAt,
	}
	if a.Avatar.Valid {
		resp.AvatarURL = "https://cdn.discordapp.com/avatars/" + a.DiscordID + "/" + a.Avatar.String + ".png?size=64"
	}
	return resp
}

func (s *Server) handleListRequests(w http.ResponseWriter, r *http.Request) {
	reqs, err := s.store.ListPendingAccountRequests(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]accountRequestResponse, len(reqs))
	for i, a := range reqs {
		out[i] = toAccountRequestResponse(a)
	}
	writeJSON(w, http.StatusOK, out)
}

// pendingRequest loads the request named in the path, writing an error unless it is pending.
func (s *Server) pendingRequest(w http.ResponseWriter, r *http.Request) (db.AccountRequest, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "demande introuvable")
		return db.AccountRequest{}, false
	}
	req, err := s.store.GetAccountRequest(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && req.Status != "pending") {
		writeError(w, http.StatusNotFound, "demande introuvable")
		return db.AccountRequest{}, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return db.AccountRequest{}, false
	}
	return req, true
}

type acceptRequest struct {
	Role string `json:"role"`
}

func (s *Server) handleAcceptRequest(w http.ResponseWriter, r *http.Request) {
	var body acceptRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	// The superadmin is the first admin only; it can never be granted through a request.
	if body.Role != "user" && body.Role != "admin" {
		writeError(w, http.StatusBadRequest, "rôle invalide")
		return
	}
	req, ok := s.pendingRequest(w, r)
	if !ok {
		return
	}
	var user db.User
	err := s.store.InTx(r.Context(), func(q *db.Queries) error {
		var err error
		user, err = q.CreateDiscordUser(r.Context(), db.CreateDiscordUserParams{
			DiscordID: nullString(req.DiscordID), DisplayName: req.DisplayName, Email: req.Email,
			Role: body.Role, CreatedAt: time.Now().Unix(),
		})
		if err != nil {
			return err
		}
		return q.DeleteAccountRequest(r.Context(), req.ID)
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.logger.Info("account request accepted", "user", user.DisplayName, "role", user.Role, "by", currentUser(r).DisplayName)
	writeJSON(w, http.StatusOK, toUserResponse(user))
}

type refuseRequest struct {
	Reason string `json:"reason"`
}

func (s *Server) handleRefuseRequest(w http.ResponseWriter, r *http.Request) {
	var body refuseRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	req, ok := s.pendingRequest(w, r)
	if !ok {
		return
	}
	if err := s.store.RefuseAccountRequest(r.Context(), db.RefuseAccountRequestParams{
		Reason: nullString(strings.TrimSpace(body.Reason)), DecidedAt: nullInt(time.Now().Unix()), ID: req.ID,
	}); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.logger.Info("account request refused", "discord_user", req.Username, "by", currentUser(r).DisplayName)
	w.WriteHeader(http.StatusNoContent)
}

type discordSettingsResponse struct {
	ClientID    string `json:"clientId"`
	HasSecret   bool   `json:"hasSecret"`
	RedirectURL string `json:"redirectUrl"`
}

func (s *Server) handleGetDiscordSettings(w http.ResponseWriter, r *http.Request) {
	id, err := s.store.GetSetting(r.Context(), settingDiscordClientID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		s.internalError(w, r, err)
		return
	}
	_, err = s.store.GetSetting(r.Context(), settingDiscordClientSecret)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		s.internalError(w, r, err)
		return
	}
	hasSecret := err == nil
	redirect, err := s.discordRedirectURL(r.Context(), r)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, discordSettingsResponse{ClientID: id, HasSecret: hasSecret, RedirectURL: redirect})
}

type putDiscordSettings struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"` // empty keeps the stored secret
}

// handlePutDiscordSettings saves the Discord application, or disables Discord sign-in when the client ID is empty.
func (s *Server) handlePutDiscordSettings(w http.ResponseWriter, r *http.Request) {
	var body putDiscordSettings
	if !decodeJSON(w, r, &body) {
		return
	}
	clientID := strings.TrimSpace(body.ClientID)
	secret := strings.TrimSpace(body.ClientSecret)
	err := s.store.InTx(r.Context(), func(q *db.Queries) error {
		if clientID == "" {
			if err := q.DeleteSetting(r.Context(), settingDiscordClientID); err != nil {
				return err
			}
			return q.DeleteSetting(r.Context(), settingDiscordClientSecret)
		}
		if secret == "" {
			// Keep the stored secret, which the UI never receives back.
			if _, err := q.GetSetting(r.Context(), settingDiscordClientSecret); errors.Is(err, sql.ErrNoRows) {
				return errMissingSecret
			} else if err != nil {
				return err
			}
		}
		return s.saveDiscordConfig(r.Context(), q, clientID, secret)
	})
	if errors.Is(err, errMissingSecret) {
		writeError(w, http.StatusBadRequest, "le Client Secret est obligatoire")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.handleGetDiscordSettings(w, r)
}

var errMissingSecret = errors.New("missing client secret")

func nullInt(v int64) sql.NullInt64 {
	return sql.NullInt64{Int64: v, Valid: true}
}
