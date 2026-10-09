package api

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net"
	"net/http"

	"github.com/thomas-bsn/forgeyard/internal/auth"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

const maxBodyBytes = 1 << 20

type errorResponse struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

func (s *Server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "erreur interne")
}

// decodeJSON reads a JSON body into v, rejecting unknown fields and oversized bodies.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "requête invalide")
		return false
	}
	return true
}

// requireJSONForWrites rejects state-changing API requests that are not JSON. Browsers cannot send
// a cross-site JSON request without a CORS preflight, so this blocks CSRF from plain HTML forms.
func requireJSONForWrites(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if mediaType != "application/json" {
				writeError(w, http.StatusUnsupportedMediaType, "Content-Type application/json requis")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

type userKey struct{}

// requireUser rejects requests without a valid session and passes the user in the context.
func (s *Server) requireUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, err := auth.SessionUser(r.Context(), s.store.Queries, r)
		if errors.Is(err, auth.ErrNoSession) {
			writeError(w, http.StatusUnauthorized, "non connecté")
			return
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userKey{}, user)))
	}
}

// requireAdmin is requireUser restricted to the admin and superadmin roles.
func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return s.requireUser(func(w http.ResponseWriter, r *http.Request) {
		if !isAdmin(currentUser(r)) {
			writeError(w, http.StatusForbidden, "réservé aux admins")
			return
		}
		next(w, r)
	})
}

// requireSuperadmin is requireUser restricted to the superadmin.
func (s *Server) requireSuperadmin(next http.HandlerFunc) http.HandlerFunc {
	return s.requireUser(func(w http.ResponseWriter, r *http.Request) {
		if currentUser(r).Role != "superadmin" {
			writeError(w, http.StatusForbidden, "réservé au superadmin")
			return
		}
		next(w, r)
	})
}

func isAdmin(u db.User) bool {
	return u.Role == "admin" || u.Role == "superadmin"
}

func currentUser(r *http.Request) db.User {
	return r.Context().Value(userKey{}).(db.User)
}

// requestOrigin returns the scheme and host the browser used to reach the server.
func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// clientIP returns the direct peer address. Proxy headers are not trusted yet.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
