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
	a, ok := s.appFromPath(w, r, actRevokeBinds)
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

func (s *Server) handleRevokeStackBindMounts(w http.ResponseWriter, r *http.Request) {
	var empty struct{}
	if !decodeJSON(w, r, &empty) {
		return
	}
	st, ok := s.stackFromPath(w, r, actRevokeBinds)
	if !ok {
		return
	}
	apps, err := s.store.ListStackApps(r.Context(), st.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	var affected []db.App
	for _, a := range apps {
		if len(appBinds(a)) > 0 {
			affected = append(affected, a)
		}
	}
	if len(affected) == 0 {
		writeError(w, http.StatusConflict, "ce projet n'utilise pas de bind mounts")
		return
	}
	now := time.Now().Unix()
	err = s.store.InTx(r.Context(), func(q *db.Queries) error {
		for _, a := range affected {
			if _, e := q.RevokeAppBindMounts(r.Context(), db.RevokeAppBindMountsParams{UpdatedAt: now, ID: a.ID}); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	for _, a := range affected {
		if a.BindAuthorized != 0 {
			s.appEvent(a.ID, eventWarning, "Autorisation de bind mounts révoquée pour le projet par "+currentUser(r).DisplayName)
		}
	}
	s.push(r.Context(), st.NodeID)
	st, err = s.store.GetStack(r.Context(), st.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.writeStack(w, r, http.StatusOK, st)
}
