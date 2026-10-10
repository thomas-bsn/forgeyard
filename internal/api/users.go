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

type accountResponse struct {
	ID            int64  `json:"id"`
	DisplayName   string `json:"displayName"`
	Method        string `json:"method"` // "discord" or "password"
	Role          string `json:"role"`
	Disabled      bool   `json:"disabled"`
	AppsSuspended bool   `json:"appsSuspended"`
	AppCount      int64  `json:"appCount"`
	AvatarURL     string `json:"avatarUrl,omitempty"`
	CreatedAt     int64  `json:"createdAt"`
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.ListUsers(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]accountResponse, 0, len(rows))
	for _, u := range rows {
		method := "password"
		if u.DiscordID.Valid {
			method = "discord"
		}
		out = append(out, accountResponse{
			ID: u.ID, DisplayName: u.DisplayName, Method: method, Role: u.Role, Disabled: u.Disabled != 0,
			AppsSuspended: u.AppsSuspended != 0, AppCount: u.AppCount, CreatedAt: u.CreatedAt,
			AvatarURL: avatarURL(db.User{ID: u.ID, DiscordID: u.DiscordID, DiscordAvatar: u.DiscordAvatar, AvatarUpdatedAt: u.AvatarUpdatedAt}),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// targetUser loads the account named in the path, if the current admin may change it: never the
// superadmin, and never one's own account (an admin cannot lock themselves out).
func (s *Server) targetUser(w http.ResponseWriter, r *http.Request) (db.User, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "compte introuvable")
		return db.User{}, false
	}
	u, err := s.store.GetUserByID(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "compte introuvable")
		return db.User{}, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return db.User{}, false
	}
	switch {
	case u.Role == "superadmin":
		writeError(w, http.StatusForbidden, "le superadmin ne peut pas être modifié")
		return db.User{}, false
	case u.ID == currentUser(r).ID:
		writeError(w, http.StatusForbidden, "vous ne pouvez pas modifier votre propre compte ici")
		return db.User{}, false
	}
	return u, true
}

type updateUserRequest struct {
	Role          *string `json:"role"`
	Disabled      *bool   `json:"disabled"`
	AppsSuspended *bool   `json:"appsSuspended"`
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// handleUpdateUser changes an account's role, disables it, or suspends its apps. Suspending stops all its
// apps; lifting it lets them be started again, without starting them.
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, ok := s.targetUser(w, r)
	if !ok {
		return
	}
	var body updateUserRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Role != nil && *body.Role != "user" && *body.Role != "admin" {
		writeError(w, http.StatusBadRequest, "rôle inconnu : user ou admin")
		return
	}
	actor := currentUser(r).DisplayName
	var stopped []db.App
	err := s.store.InTx(ctx, func(q *db.Queries) error {
		if body.Role != nil {
			if err := q.SetUserRole(ctx, db.SetUserRoleParams{Role: *body.Role, ID: u.ID}); err != nil {
				return err
			}
		}
		if body.Disabled != nil {
			if err := q.SetUserDisabled(ctx, db.SetUserDisabledParams{Disabled: boolInt(*body.Disabled), ID: u.ID}); err != nil {
				return err
			}
			if *body.Disabled {
				if err := q.DeleteUserSessions(ctx, u.ID); err != nil {
					return err
				}
			}
		}
		if body.AppsSuspended != nil {
			if err := q.SetUserAppsSuspended(ctx, db.SetUserAppsSuspendedParams{AppsSuspended: boolInt(*body.AppsSuspended), ID: u.ID}); err != nil {
				return err
			}
			if *body.AppsSuspended && u.AppsSuspended == 0 {
				apps, err := q.ListAppsByOwnerID(ctx, u.ID)
				if err != nil {
					return err
				}
				stopped = apps
				if err := q.StopAppsByOwner(ctx, db.StopAppsByOwnerParams{UpdatedAt: time.Now().Unix(), OwnerID: u.ID}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	pushed := map[int64]bool{}
	for _, a := range stopped {
		s.appEvent(a.ID, eventWarning, "Suspendue par "+actor)
		if !pushed[a.NodeID] {
			s.push(ctx, a.NodeID)
			pushed[a.NodeID] = true
		}
	}
	var changes []string
	if body.Role != nil {
		changes = append(changes, "role="+*body.Role)
	}
	if body.Disabled != nil {
		changes = append(changes, "disabled="+strconv.FormatBool(*body.Disabled))
	}
	if body.AppsSuspended != nil {
		changes = append(changes, "apps_suspended="+strconv.FormatBool(*body.AppsSuspended))
	}
	s.logger.Info("user changed", "user", u.DisplayName, "changes", strings.Join(changes, " "), "by", actor)
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteUser deletes an account with its apps, their containers and DNS records.
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, ok := s.targetUser(w, r)
	if !ok {
		return
	}
	apps, err := s.store.ListAppsByOwnerID(ctx, u.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	for _, a := range apps {
		if err := s.removeApp(ctx, a); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	if err := s.store.DeleteUser(ctx, u.ID); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.logger.Info("user deleted", "user", u.DisplayName, "apps", len(apps), "by", currentUser(r).DisplayName)
	w.WriteHeader(http.StatusNoContent)
}

type generalSettings struct {
	Name      string `json:"name"`
	PublicURL string `json:"publicUrl"`
}

// handlePutGeneralSettings changes the instance's name and Forgeyard's address.
func (s *Server) handlePutGeneralSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body generalSettings
	if !decodeJSON(w, r, &body) {
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || len([]rune(name)) > 64 {
		writeError(w, http.StatusBadRequest, "le nom doit faire entre 1 et 64 caractères")
		return
	}
	publicURL, ok := normalizePublicURL(body.PublicURL)
	if !ok {
		writeError(w, http.StatusBadRequest, "adresse de Forgeyard invalide : par exemple https://forgeyard.mondomaine.com")
		return
	}
	if err := s.store.InTx(ctx, func(q *db.Queries) error {
		return storeSettings(ctx, q, map[string]string{settingInstanceName: name, settingPublicURL: publicURL})
	}); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.logger.Info("general settings changed", "name", name, "public_url", publicURL, "by", currentUser(r).DisplayName)
	writeJSON(w, http.StatusOK, generalSettings{Name: name, PublicURL: publicURL})
}
