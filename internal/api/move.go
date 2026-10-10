package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

// Moving an app to another node: it starts there while it keeps running on the previous node, which drops
// it once the new node reports it online. When the two nodes have different public IPs, the DNS record
// changes at once and the previous node keeps serving for dnsSettle, while resolvers still give out the
// old address.

const dnsSettle = 6 * time.Minute // the DNS record's TTL is 5 minutes

func (s *Server) handleMoveApp(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a, ok := s.appFromPath(w, r)
	if !ok {
		return
	}
	var body struct {
		NodeID int64 `json:"nodeId"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if a.MovingFrom != 0 {
		writeError(w, http.StatusConflict, "cette app est déjà en train de changer de node")
		return
	}
	if body.NodeID == a.NodeID {
		writeError(w, http.StatusBadRequest, "l'app tourne déjà sur ce node")
		return
	}
	target, err := s.pickNode(ctx, body.NodeID)
	if errors.Is(err, errNodeUnavailable) || errors.Is(err, errNoNode) {
		writeError(w, http.StatusConflict, "ce node n'est pas en ligne")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	from, err := s.store.GetNode(ctx, a.NodeID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	c, err := s.loadDNSConfig(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	// A stopped app has nothing to keep running: it just changes node.
	movingFrom := int64(0)
	if a.Running != 0 {
		movingFrom = a.NodeID
	}
	now := time.Now().Unix()
	moved, err := s.store.MoveApp(ctx, db.MoveAppParams{MovingFrom: movingFrom, NodeID: target.ID, MovedAt: now, UpdatedAt: now, ID: a.ID})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if a.DnsName != "" && nodeIP(target, c) != nodeIP(from, c) {
		if p, err := s.dnsProvider(c); err == nil && p != nil {
			if err := s.setAppRecord(ctx, c, p, a.DnsName, nodeIP(target, c)); err != nil {
				s.logger.Warn("pointing the moved app's DNS record failed", "app", a.Name, "err", err)
			}
		}
	}
	if movingFrom != 0 {
		s.nodes.ResetApp(a.ID)
	}
	s.appEvent(a.ID, eventInfo, "Déplacement de "+from.Name+" vers "+target.Name+" demandé par "+currentUser(r).DisplayName)
	s.logger.Info("app moving", "app", a.Name, "from", from.Name, "to", target.Name, "by", currentUser(r).DisplayName)
	s.push(ctx, target.ID)
	s.push(ctx, from.ID)
	s.writeApp(w, r, http.StatusOK, moved, false)
}

// appOnline finishes a move once the app runs on its new node: right away behind the same public IP, else
// once resolvers have caught up with the new DNS record.
func (s *Server) appOnline(appID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a, err := s.store.GetApp(ctx, appID)
	if err != nil || a.MovingFrom == 0 {
		return
	}
	wait := time.Duration(0)
	if from, err := s.store.GetNode(ctx, a.MovingFrom); err == nil {
		to, err1 := s.store.GetNode(ctx, a.NodeID)
		c, err2 := s.loadDNSConfig(ctx)
		if err1 == nil && err2 == nil && a.DnsName != "" && nodeIP(from, c) != nodeIP(to, c) {
			wait = dnsSettle - time.Since(time.Unix(a.MovedAt, 0))
		}
	}
	if wait > 0 {
		time.AfterFunc(wait, func() { s.finishMove(appID) })
		return
	}
	s.finishMove(appID)
}

func (s *Server) finishMove(appID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	before, err := s.store.GetApp(ctx, appID)
	if err != nil || before.MovingFrom == 0 {
		return
	}
	if _, err := s.store.FinishMove(ctx, appID); err != nil {
		return
	}
	name := "#"
	if n, err := s.store.GetNode(ctx, before.NodeID); err == nil {
		name = n.Name
	}
	s.appEvent(appID, eventSuccess, "Déplacée sur "+name+" : l'ancien node a arrêté son conteneur")
	s.push(ctx, before.MovingFrom)
}
