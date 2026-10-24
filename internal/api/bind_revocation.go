package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

var errBindRevoked = errors.New("l'autorisation de bind mounts est révoquée : une nouvelle approbation est nécessaire")

func (s *Server) handleRevokeAppBindMounts(w http.ResponseWriter, r *http.Request) {
	var empty struct{}
	if !decodeJSON(w, r, &empty) {
		return
	}
	a, ok := s.appFromPath(w, r)
	if !ok {
		return
	}
	if len(appBinds(a)) == 0 {
		writeError(w, http.StatusConflict, "cette app n'utilise pas de bind mounts")
		return
	}
	if a.StackID != 0 {
		writeError(w, http.StatusConflict, "révoquez l'autorisation du projet auquel appartient ce service")
		return
	}
	wasAuthorized := a.BindAuthorized != 0
	a, err := s.store.RevokeAppBindMounts(r.Context(), db.RevokeAppBindMountsParams{UpdatedAt: time.Now().Unix(), ID: a.ID})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if wasAuthorized {
		s.appEvent(a.ID, eventWarning, "Autorisation de bind mounts révoquée par "+currentUser(r).DisplayName)
	}
	s.push(r.Context(), a.NodeID)
	s.writeApp(w, r, http.StatusOK, a, false)
}
