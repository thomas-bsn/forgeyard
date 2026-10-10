package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/thomas-bsn/forgeyard/internal/auth"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

type userResponse struct {
	ID              int64  `json:"id"`
	Username        string `json:"username,omitempty"`
	DisplayName     string `json:"displayName"`
	Role            string `json:"role"`
	Method          string `json:"method"` // "discord" or "password"
	AvatarURL       string `json:"avatarUrl,omitempty"`
	CustomAvatar    bool   `json:"customAvatar"`
	BannerURL       string `json:"bannerUrl,omitempty"`
	CustomBanner    bool   `json:"customBanner"`
	AccentColor     string `json:"accentColor,omitempty"`
	ShowApps        bool   `json:"showApps"`
	ShowEmail       bool   `json:"showEmail"`
	Email           string `json:"email"`
	Bio             string `json:"bio"`
	DiscordName     string `json:"discordName,omitempty"`
	NameFromDiscord bool   `json:"nameFromDiscord"`
	CreatedAt       int64  `json:"createdAt"`
}

func toUserResponse(u db.User) userResponse {
	return userResponse{
		ID: u.ID, Username: u.Username.String, DisplayName: u.DisplayName, Role: u.Role, Method: signInMethod(u),
		AvatarURL: avatarURL(u), CustomAvatar: u.AvatarUpdatedAt > 0, Email: u.Email.String, Bio: u.Bio,
		BannerURL: bannerURL(u), CustomBanner: u.BannerUpdatedAt > 0, AccentColor: accentColor(u),
		ShowApps: u.ShowApps != 0, ShowEmail: u.ShowEmail != 0,
		DiscordName: u.DiscordName, NameFromDiscord: u.DiscordID.Valid && u.NameFromDiscord != 0, CreatedAt: u.CreatedAt,
	}
}

// bannerURL is the banner the user uploaded, else their Discord banner, else empty (the UI uses their
// profile colour).
func bannerURL(u db.User) string {
	switch {
	case u.BannerUpdatedAt > 0:
		return "/api/users/" + strconv.FormatInt(u.ID, 10) + "/banner?v=" + strconv.FormatInt(u.BannerUpdatedAt, 10)
	case u.DiscordID.Valid && u.DiscordBanner != "":
		return "https://cdn.discordapp.com/banners/" + u.DiscordID.String + "/" + u.DiscordBanner + ".png?size=600"
	}
	return ""
}

// accentColor is the Discord profile colour as #rrggbb, or empty.
func accentColor(u db.User) string {
	if u.DiscordAccent < 0 {
		return ""
	}
	return fmt.Sprintf("#%06x", u.DiscordAccent&0xffffff)
}

func signInMethod(u db.User) string {
	if u.DiscordID.Valid {
		return "discord"
	}
	return "password"
}

// avatarURL is the picture the user uploaded, else their Discord avatar, else empty (the UI shows an
// initial). The version in an uploaded picture's URL lets browsers cache it until it changes.
func avatarURL(u db.User) string {
	switch {
	case u.AvatarUpdatedAt > 0:
		return "/api/users/" + strconv.FormatInt(u.ID, 10) + "/avatar?v=" + strconv.FormatInt(u.AvatarUpdatedAt, 10)
	case u.DiscordID.Valid && u.DiscordAvatar != "":
		return "https://cdn.discordapp.com/avatars/" + u.DiscordID.String + "/" + u.DiscordAvatar + ".png?size=128"
	}
	return ""
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
