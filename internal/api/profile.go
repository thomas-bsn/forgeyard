package api

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/auth"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

// maxAvatarBytes bounds an uploaded picture; the browser resizes it to 256×256 first. A banner, resized
// to 1500×500, gets more room.
const (
	maxAvatarBytes = 512 << 10
	maxBannerBytes = 700 << 10
)

// avatarTypes are the formats accepted for an uploaded picture: raster images only, never SVG, which
// could carry scripts.
var avatarTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true, "image/gif": true}

type profileRequest struct {
	DisplayName     string `json:"displayName"`
	NameFromDiscord bool   `json:"nameFromDiscord"`
	Bio             string `json:"bio"`
	Email           string `json:"email"`
	ShowApps        bool   `json:"showApps"`
	ShowEmail       bool   `json:"showEmail"`
}

// handlePutProfile changes the current user's display name, description and email.
func (s *Server) handlePutProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := currentUser(r)
	var body profileRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	followDiscord := body.NameFromDiscord && u.DiscordID.Valid
	name := strings.TrimSpace(body.DisplayName)
	if followDiscord && u.DiscordName != "" {
		name = u.DiscordName
	}
	if name == "" || len([]rune(name)) > 64 {
		writeError(w, http.StatusBadRequest, "le nom affiché doit faire entre 1 et 64 caractères")
		return
	}
	bio := strings.TrimSpace(body.Bio)
	if len([]rune(bio)) > 280 {
		writeError(w, http.StatusBadRequest, "la description fait au plus 280 caractères")
		return
	}
	email := strings.TrimSpace(body.Email)
	if email != "" {
		if a, err := mail.ParseAddress(email); err != nil || a.Address != email {
			writeError(w, http.StatusBadRequest, "adresse email invalide")
			return
		}
	}
	if err := s.store.UpdateProfile(ctx, db.UpdateProfileParams{
		DisplayName: name, NameFromDiscord: boolInt(followDiscord), Bio: bio, Email: nullString(email),
		ShowApps: boolInt(body.ShowApps), ShowEmail: boolInt(body.ShowEmail && email != ""), ID: u.ID,
	}); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.writeMe(w, r)
}

func (s *Server) writeMe(w http.ResponseWriter, r *http.Request) {
	u, err := s.store.GetUserByID(r.Context(), currentUser(r).ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toUserResponse(u))
}

// readImage decodes an uploaded image sent as a data URL, checking its size and its real format.
func readImage(w http.ResponseWriter, r *http.Request, maxBytes int) ([]byte, string, bool) {
	var body struct {
		Image string `json:"image"`
	}
	if !decodeJSON(w, r, &body) {
		return nil, "", false
	}
	_, encoded, ok := strings.Cut(body.Image, ";base64,")
	if !ok {
		writeError(w, http.StatusBadRequest, "image invalide")
		return nil, "", false
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) == 0 {
		writeError(w, http.StatusBadRequest, "image invalide")
		return nil, "", false
	}
	if len(data) > maxBytes {
		writeError(w, http.StatusBadRequest, "image trop lourde ("+strconv.Itoa(maxBytes>>10)+" Ko au plus)")
		return nil, "", false
	}
	// The type comes from the bytes, not from what the browser claims.
	contentType := http.DetectContentType(data)
	if !avatarTypes[contentType] {
		writeError(w, http.StatusBadRequest, "format refusé : PNG, JPEG, WebP ou GIF")
		return nil, "", false
	}
	return data, contentType, true
}

// serveImage sends an uploaded image so that a browser can only display it.
func serveImage(w http.ResponseWriter, contentType string, data []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	// The URL changes with the image, so it can be cached for long.
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Write(data)
}

// handlePutAvatar stores a picture the user uploaded, sent as a data URL.
func (s *Server) handlePutAvatar(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data, contentType, ok := readImage(w, r, maxAvatarBytes)
	if !ok {
		return
	}
	u := currentUser(r)
	if err := s.store.InTx(ctx, func(q *db.Queries) error {
		if err := q.SetAvatar(ctx, db.SetAvatarParams{UserID: u.ID, ContentType: contentType, Data: data}); err != nil {
			return err
		}
		return q.SetAvatarUpdatedAt(ctx, db.SetAvatarUpdatedAtParams{AvatarUpdatedAt: time.Now().UnixMilli(), ID: u.ID})
	}); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.writeMe(w, r)
}

// handleDeleteAvatar goes back to the Discord avatar, or to the initial.
func (s *Server) handleDeleteAvatar(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := currentUser(r)
	if err := s.store.InTx(ctx, func(q *db.Queries) error {
		if err := q.DeleteAvatar(ctx, u.ID); err != nil {
			return err
		}
		return q.SetAvatarUpdatedAt(ctx, db.SetAvatarUpdatedAtParams{AvatarUpdatedAt: 0, ID: u.ID})
	}); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.writeMe(w, r)
}

// handleGetAvatar serves an uploaded picture to signed-in users.
func (s *Server) handleGetAvatar(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a, err := s.store.GetAvatar(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	serveImage(w, a.ContentType, a.Data)
}

// handlePutBanner stores a banner the user uploaded, in place of their Discord banner.
func (s *Server) handlePutBanner(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data, contentType, ok := readImage(w, r, maxBannerBytes)
	if !ok {
		return
	}
	u := currentUser(r)
	if err := s.store.InTx(ctx, func(q *db.Queries) error {
		if err := q.SetBanner(ctx, db.SetBannerParams{UserID: u.ID, ContentType: contentType, Data: data}); err != nil {
			return err
		}
		return q.SetBannerUpdatedAt(ctx, db.SetBannerUpdatedAtParams{BannerUpdatedAt: time.Now().UnixMilli(), ID: u.ID})
	}); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.writeMe(w, r)
}

// handleDeleteBanner goes back to the Discord banner, or to the profile colour.
func (s *Server) handleDeleteBanner(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := currentUser(r)
	if err := s.store.InTx(ctx, func(q *db.Queries) error {
		if err := q.DeleteBanner(ctx, u.ID); err != nil {
			return err
		}
		return q.SetBannerUpdatedAt(ctx, db.SetBannerUpdatedAtParams{BannerUpdatedAt: 0, ID: u.ID})
	}); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.writeMe(w, r)
}

// handleGetBanner serves an uploaded banner to signed-in users.
func (s *Server) handleGetBanner(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	b, err := s.store.GetBanner(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	serveImage(w, b.ContentType, b.Data)
}

// handlePutPassword changes the password of an account that signs in with one.
func (s *Server) handlePutPassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := currentUser(r)
	if !u.PasswordHash.Valid {
		writeError(w, http.StatusBadRequest, "ce compte se connecte avec Discord : il n'a pas de mot de passe")
		return
	}
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	ip := clientIP(r)
	if !s.limiter.Allowed(ip) {
		writeError(w, http.StatusTooManyRequests, "trop de tentatives, réessayez dans quelques minutes")
		return
	}
	if ok, err := auth.VerifyPassword(body.Current, u.PasswordHash.String); err != nil || !ok {
		s.limiter.Fail(ip)
		writeError(w, http.StatusForbidden, "mot de passe actuel incorrect")
		return
	}
	if len(body.New) < 12 {
		writeError(w, http.StatusBadRequest, "le nouveau mot de passe doit faire au moins 12 caractères")
		return
	}
	hash, err := auth.HashPassword(body.New)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	// Other devices are signed out: whoever knew the old password loses access.
	if err := s.store.InTx(ctx, func(q *db.Queries) error {
		if err := q.SetPasswordHash(ctx, db.SetPasswordHashParams{PasswordHash: nullString(hash), ID: u.ID}); err != nil {
			return err
		}
		return q.DeleteOtherSessions(ctx, db.DeleteOtherSessionsParams{UserID: u.ID, TokenHash: auth.SessionToken(r)})
	}); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.logger.Info("password changed", "user", u.DisplayName)
	w.WriteHeader(http.StatusNoContent)
}

type sessionResponse struct {
	ID        string `json:"id"`
	CreatedAt int64  `json:"createdAt"`
	ExpiresAt int64  `json:"expiresAt"`
	IP        string `json:"ip"`
	UserAgent string `json:"userAgent"`
	Current   bool   `json:"current"`
}

// sessionID shows a session without its token hash: a prefix is enough to tell sessions apart.
func sessionID(tokenHash string) string {
	return tokenHash[:min(16, len(tokenHash))]
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	rows, err := s.store.ListUserSessions(r.Context(), db.ListUserSessionsParams{UserID: u.ID, ExpiresAt: time.Now().Unix()})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	current := auth.SessionToken(r)
	out := make([]sessionResponse, 0, len(rows))
	for _, ss := range rows {
		out = append(out, sessionResponse{
			ID: sessionID(ss.TokenHash), CreatedAt: ss.CreatedAt, ExpiresAt: ss.ExpiresAt, IP: ss.Ip, UserAgent: ss.UserAgent,
			Current: ss.TokenHash == current,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDeleteSession signs out one of the user's sessions, or all the others with the id "others".
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := currentUser(r)
	current := auth.SessionToken(r)
	id := r.PathValue("id")
	if id == "others" {
		if err := s.store.DeleteOtherSessions(ctx, db.DeleteOtherSessionsParams{UserID: u.ID, TokenHash: current}); err != nil {
			s.internalError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	rows, err := s.store.ListUserSessions(ctx, db.ListUserSessionsParams{UserID: u.ID, ExpiresAt: time.Now().Unix()})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	for _, ss := range rows {
		if sessionID(ss.TokenHash) == id && ss.TokenHash != current {
			if err := s.store.DeleteUserSession(ctx, db.DeleteUserSessionParams{UserID: u.ID, TokenHash: ss.TokenHash}); err != nil {
				s.internalError(w, r, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	writeError(w, http.StatusNotFound, "session introuvable")
}
