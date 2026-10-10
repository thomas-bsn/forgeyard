package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/nodes"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

// Event kinds, which the UI colors.
const (
	eventInfo    = "info"
	eventSuccess = "success"
	eventWarning = "warning"
	eventError   = "error"
)

// appEvent records what happened to an app. It never fails the request that caused it.
func (s *Server) appEvent(appID int64, kind, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := s.store.AddAppEvent(ctx, db.AddAppEventParams{AppID: appID, At: time.Now().Unix(), Kind: kind, Message: message})
	if err == nil {
		err = s.store.PruneAppEvents(ctx, appID)
	}
	if err != nil {
		s.logger.Warn("recording an app event failed", "app_id", appID, "err", err)
	}
}

// onAppStateChange turns the state changes agents report into events: when an app comes up, crashes or
// restarts in a loop.
func (s *Server) onAppStateChange(appID int64, from, to *agentpb.AppStatus) {
	if n, why := crashOf(from, to); n > 0 {
		go s.crashed(appID, n, why)
	}
	if from.GetState() == to.GetState() {
		return // only the restart count changed
	}
	switch to.GetState() {
	case "running":
		s.appEvent(appID, eventSuccess, "En ligne")
	case "exited":
		msg := fmt.Sprintf("Plantée (code %d)", to.GetExitCode())
		if to.GetOomKilled() {
			msg = "Plantée : manque de mémoire"
		}
		s.appEvent(appID, eventError, msg)
	case "restarting":
		if from.GetState() != "restarting" {
			s.appEvent(appID, eventWarning, "Redémarre en boucle")
		}
	case "error":
		s.appEvent(appID, eventError, "Erreur : "+to.GetError())
	}
}

type appEventResponse struct {
	At      int64  `json:"at"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

func (s *Server) handleAppEvents(w http.ResponseWriter, r *http.Request) {
	a, ok := s.appFromPath(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.store.ListAppEvents(r.Context(), db.ListAppEventsParams{AppID: a.ID, Limit: int64(limit)})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]appEventResponse, 0, len(rows))
	for _, e := range rows {
		out = append(out, appEventResponse{At: e.At, Kind: e.Kind, Message: e.Message})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAppUsage returns an app's CPU and memory use over the last hour.
func (s *Server) handleAppUsage(w http.ResponseWriter, r *http.Request) {
	a, ok := s.appFromPath(w, r)
	if !ok {
		return
	}
	samples := s.nodes.AppUsage(a.ID)
	if samples == nil {
		samples = []nodes.UsageSample{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"memoryLimitBytes": a.MemoryMb << 20,
		"samples":          samples,
	})
}
