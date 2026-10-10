package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

// The instance's icon replaces Forgeyard's logo in the UI and is its favicon. It is public, since the
// sign-in page shows it.

const maxIconBytes = 512 << 10

// setupIcon decodes the icon chosen in the wizard, if any.
func setupIcon(w http.ResponseWriter, dataURL string) ([]byte, string, bool) {
	if dataURL == "" {
		return nil, "", true
	}
	data, contentType, err := decodeDataURL(dataURL, maxIconBytes)
	if err != nil {
		writeError(w, http.StatusBadRequest, "icône : "+err.Error())
		return nil, "", false
	}
	return data, contentType, true
}

// iconURL is where the UI loads the instance's icon, or "" when it has none.
func (s *Server) iconURL(r *http.Request) string {
	icon, err := s.store.GetInstanceIcon(r.Context())
	if err != nil {
		return ""
	}
	return "/api/instance/icon?v=" + strconv.FormatInt(icon.UpdatedAt, 10)
}

func (s *Server) handleGetIcon(w http.ResponseWriter, r *http.Request) {
	icon, err := s.store.GetInstanceIcon(r.Context())
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	serveImage(w, icon.ContentType, icon.Data)
}

func (s *Server) handlePutIcon(w http.ResponseWriter, r *http.Request) {
	data, contentType, ok := readImage(w, r, maxIconBytes)
	if !ok {
		return
	}
	if err := s.store.PutInstanceIcon(r.Context(), db.PutInstanceIconParams{ContentType: contentType, Data: data, UpdatedAt: time.Now().UnixMilli()}); err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"iconUrl": s.iconURL(r)})
}

func (s *Server) handleDeleteIcon(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteInstanceIcon(r.Context()); err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
