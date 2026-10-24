package api

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

type bindRequestEvent string

const (
	bindRequestSubmitted bindRequestEvent = "submitted"
	bindRequestApproved  bindRequestEvent = "approved"
	bindRequestRefused   bindRequestEvent = "refused"
	bindRequestCancelled bindRequestEvent = "cancelled"
	bindRequestCreated   bindRequestEvent = "created"
	bindRequestFailed    bindRequestEvent = "failed"
)

// Only identifiers and fixed text cross the Discord boundary. In particular, names, configuration,
// refusal reasons and agent errors can contain secrets and must stay on the authenticated detail page.
// This hook follows requests, independent of the kind of app or project they describe.
func (s *Server) notifyBindRequest(row db.BindRequest, event bindRequestEvent) {
	title, text, color := "", "", colorInfo
	switch event {
	case bindRequestSubmitted:
		title, text = "Demande de bind mounts soumise", "Une décision du superadmin est nécessaire."
	case bindRequestApproved:
		title, text, color = "Demande de bind mounts approuvée", "Le superadmin a approuvé les accès demandés. La création est en cours.", colorSuccess
	case bindRequestRefused:
		title, text, color = "Demande de bind mounts refusée", "Le superadmin a refusé la demande. Le motif est disponible dans Forgeyard.", colorWarning
	case bindRequestCancelled:
		title, text = "Demande de bind mounts annulée", "Le demandeur a annulé sa demande."
	case bindRequestCreated:
		title, text, color = "Création après approbation enregistrée", "La création demandée est enregistrée. Consultez le suivi du déploiement dans Forgeyard.", colorSuccess
	case bindRequestFailed:
		title, text, color = "Création après approbation en échec", "La demande n'a pas pu aboutir. Le détail et la relance par le superadmin sont disponibles dans Forgeyard.", colorError
	default:
		return
	}
	id, authorID := row.ID, row.AuthorID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, events, err := s.adminNotifySettings(ctx)
		if err != nil || !events["requests"] {
			return
		}
		n := notification{Title: title, Text: fmt.Sprintf("Demande #%d · membre #%d. %s", id, authorID, text), Path: "#/bind-requests/" + strconv.FormatInt(id, 10), Color: color}
		s.notifyUser(authorID, n)
		n.Path = "#/bind-requests/review/" + strconv.FormatInt(id, 10)
		if event == bindRequestSubmitted {
			n.Title = "Demande de bind mounts à examiner par le superadmin"
		}
		if superadmin, err := s.store.GetSuperadmin(ctx); err == nil && superadmin.ID != authorID {
			s.notifyUser(superadmin.ID, n)
		}
		// The configured requests/support channel can be shared. Its message is deliberately generic;
		// the linked review and decisions remain accessible only to the superadmin.
		s.notifyAdmins("requests", n)
	}()
}
