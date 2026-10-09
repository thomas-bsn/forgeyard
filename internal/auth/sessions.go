package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

const (
	// SessionCookie is the name of the session cookie.
	SessionCookie = "forgeyard_session"
	// SessionTTL is how long a session stays valid.
	SessionTTL = 30 * 24 * time.Hour
)

// ErrNoSession means the request carries no valid session.
var ErrNoSession = errors.New("no valid session")

// CreateSession stores a new session for userID and sets its cookie on w.
func CreateSession(ctx context.Context, q *db.Queries, w http.ResponseWriter, r *http.Request, userID int64) error {
	token := NewToken()
	now := time.Now()
	expires := now.Add(SessionTTL)
	if err := q.CreateSession(ctx, db.CreateSessionParams{
		TokenHash: HashToken(token),
		UserID:    userID,
		CreatedAt: now.Unix(),
		ExpiresAt: expires.Unix(),
	}); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// SessionUser returns the user owning the request's session.
func SessionUser(ctx context.Context, q *db.Queries, r *http.Request) (db.User, error) {
	cookie, err := r.Cookie(SessionCookie)
	if err != nil {
		return db.User{}, ErrNoSession
	}
	user, err := q.GetSessionUser(ctx, db.GetSessionUserParams{
		TokenHash: HashToken(cookie.Value),
		ExpiresAt: time.Now().Unix(),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return db.User{}, ErrNoSession
	}
	return user, err
}

// DestroySession deletes the request's session, if any, and clears its cookie.
func DestroySession(ctx context.Context, q *db.Queries, w http.ResponseWriter, r *http.Request) error {
	if cookie, err := r.Cookie(SessionCookie); err == nil {
		if err := q.DeleteSession(ctx, HashToken(cookie.Value)); err != nil {
			return err
		}
	}
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", MaxAge: -1})
	return nil
}
