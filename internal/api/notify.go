package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

// Notifications go to Discord webhooks: one channel for the admins, and each user's own for their apps.

const (
	settingNotifyWebhook = "notify_webhook" // encrypted
	settingNotifyEvents  = "notify_events"  // comma-separated: requests, crashes, nodes
	labelUserWebhook     = "user_notify_webhook"

	// An app crashing crashLimit times within crashWindow is stopped.
	crashLimit  = 3
	crashWindow = 5 * time.Minute
	// A user hears about a crash of an app at most once per crashNotifyEvery.
	crashNotifyEvery = 10 * time.Minute
	// A node must stay offline this long before the admins are told, so short drops stay quiet.
	nodeOfflineGrace = time.Minute
)

var notifyEventKinds = []string{"requests", "crashes", "nodes"}

// notification is one message, shown on Discord as an embed.
type notification struct {
	Title string
	Text  string
	Path  string // in Forgeyard, e.g. "#/apps/3"
	Color int
}

const (
	colorInfo    = 0x4f46e5
	colorSuccess = 0x16a34a
	colorWarning = 0xd97706
	colorError   = 0xdc2626
)

// validWebhook accepts only Discord's webhook addresses, so Forgeyard cannot be made to post elsewhere.
func validWebhook(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil {
		return false
	}
	switch u.Host {
	case "discord.com", "discordapp.com", "ptb.discord.com", "canary.discord.com":
	default:
		return false
	}
	return strings.HasPrefix(u.Path, "/api/webhooks/")
}

// sendWebhook posts a notification to a Discord webhook.
func (s *Server) sendWebhook(ctx context.Context, webhook string, n notification) error {
	embed := map[string]any{
		"title":       n.Title,
		"description": n.Text,
		"color":       n.Color,
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
	}
	if base, err := s.store.GetSetting(ctx, settingPublicURL); err == nil && n.Path != "" {
		embed["url"] = strings.TrimRight(base, "/") + "/" + n.Path
	}
	name, err := s.store.GetSetting(ctx, settingInstanceName)
	if err != nil || name == "" {
		name = defaultInstanceName
	}
	body, _ := json.Marshal(map[string]any{"username": name, "embeds": []any{embed}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("Discord a répondu %d", resp.StatusCode)
	}
	return nil
}

// notifyAdmins sends a notification to the admins' channel, when it is set and wants this kind of event.
// It never blocks nor fails the caller.
func (s *Server) notifyAdmins(kind string, n notification) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		webhook, events, err := s.adminNotifySettings(ctx)
		if err != nil || webhook == "" || !events[kind] {
			return
		}
		if err := s.sendWebhook(ctx, webhook, n); err != nil {
			s.logger.Warn("notifying the admins failed", "kind", kind, "err", err)
		}
	}()
}

// notifyUser sends a notification to a user's own webhook, if they set one.
func (s *Server) notifyUser(userID int64, n notification) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		u, err := s.store.GetUserByID(ctx, userID)
		if err != nil || u.NotifyWebhook == "" {
			return
		}
		webhook, err := s.secrets.Decrypt(u.NotifyWebhook, labelUserWebhook)
		if err != nil {
			return
		}
		if err := s.sendWebhook(ctx, webhook, n); err != nil {
			s.logger.Warn("notifying a user failed", "user", u.DisplayName, "err", err)
		}
	}()
}

func (s *Server) adminNotifySettings(ctx context.Context) (string, map[string]bool, error) {
	events := map[string]bool{}
	if v, err := s.store.GetSetting(ctx, settingNotifyEvents); err == nil {
		for _, k := range strings.Split(v, ",") {
			events[k] = true
		}
	} else {
		for _, k := range notifyEventKinds {
			events[k] = true
		}
	}
	sealed, err := s.store.GetSetting(ctx, settingNotifyWebhook)
	if err != nil || sealed == "" {
		return "", events, nil
	}
	webhook, err := s.secrets.Decrypt(sealed, settingNotifyWebhook)
	return webhook, events, err
}

type notifySettings struct {
	WebhookSet bool            `json:"webhookSet"`
	Webhook    string          `json:"webhook,omitempty"` // write only: empty keeps the current one
	Clear      bool            `json:"clear,omitempty"`
	Events     map[string]bool `json:"events"`
}

func (s *Server) handleGetNotifySettings(w http.ResponseWriter, r *http.Request) {
	webhook, events, err := s.adminNotifySettings(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := notifySettings{WebhookSet: webhook != "", Events: map[string]bool{}}
	for _, k := range notifyEventKinds {
		out.Events[k] = events[k]
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePutNotifySettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body notifySettings
	if !decodeJSON(w, r, &body) {
		return
	}
	webhook := strings.TrimSpace(body.Webhook)
	if webhook != "" && !validWebhook(webhook) {
		writeError(w, http.StatusBadRequest, "ce n'est pas l'adresse d'un webhook Discord (https://discord.com/api/webhooks/…)")
		return
	}
	var kinds []string
	for _, k := range notifyEventKinds {
		if body.Events[k] {
			kinds = append(kinds, k)
		}
	}
	values := map[string]string{settingNotifyEvents: strings.Join(kinds, ",")}
	switch {
	case body.Clear:
		values[settingNotifyWebhook] = ""
	case webhook != "":
		sealed, err := s.secrets.Encrypt(webhook, settingNotifyWebhook)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		values[settingNotifyWebhook] = sealed
	}
	if err := s.store.InTx(ctx, func(q *db.Queries) error { return storeSettings(ctx, q, values) }); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.logger.Info("notification settings changed", "events", values[settingNotifyEvents], "by", currentUser(r).DisplayName)
	s.handleGetNotifySettings(w, r)
}

func (s *Server) handleTestAdminNotify(w http.ResponseWriter, r *http.Request) {
	webhook, _, err := s.adminNotifySettings(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.testWebhook(w, r, webhook)
}

func (s *Server) testWebhook(w http.ResponseWriter, r *http.Request, webhook string) {
	if webhook == "" {
		writeError(w, http.StatusBadRequest, "aucun webhook enregistré")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := s.sendWebhook(ctx, webhook, notification{
		Title: "Notifications Forgeyard", Text: "Ça marche : les alertes arriveront ici.", Color: colorSuccess,
	}); err != nil {
		writeError(w, http.StatusBadGateway, "envoi impossible : "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handlePutMyWebhook sets or clears the current user's own webhook.
func (s *Server) handlePutMyWebhook(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Webhook string `json:"webhook"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	webhook := strings.TrimSpace(body.Webhook)
	sealed := ""
	if webhook != "" {
		if !validWebhook(webhook) {
			writeError(w, http.StatusBadRequest, "ce n'est pas l'adresse d'un webhook Discord (https://discord.com/api/webhooks/…)")
			return
		}
		var err error
		if sealed, err = s.secrets.Encrypt(webhook, labelUserWebhook); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	if err := s.store.SetUserNotifyWebhook(r.Context(), db.SetUserNotifyWebhookParams{NotifyWebhook: sealed, ID: currentUser(r).ID}); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.writeMe(w, r)
}

func (s *Server) handleTestMyWebhook(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u.NotifyWebhook == "" {
		writeError(w, http.StatusBadRequest, "aucun webhook enregistré")
		return
	}
	webhook, err := s.secrets.Decrypt(u.NotifyWebhook, labelUserWebhook)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.testWebhook(w, r, webhook)
}

// crashed counts crashes of an app and stops it after crashLimit crashes within crashWindow.
func (s *Server) crashed(appID int64, count int, why string) {
	now := time.Now()
	s.mu.Lock()
	recent := s.crashes[appID][:0]
	for _, t := range s.crashes[appID] {
		if now.Sub(t) < crashWindow {
			recent = append(recent, t)
		}
	}
	for range count {
		recent = append(recent, now)
	}
	s.crashes[appID] = recent
	tellUser := now.Sub(s.crashNotified[appID]) >= crashNotifyEvery
	if tellUser {
		s.crashNotified[appID] = now
	}
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, err := s.store.GetApp(ctx, appID)
	if err != nil {
		return
	}
	path := "#/apps/" + strconv.FormatInt(a.ID, 10)
	if len(recent) >= crashLimit {
		suspended, err := s.store.SuspendCrashingApp(ctx, db.SuspendCrashingAppParams{UpdatedAt: now.Unix(), ID: appID})
		if err == nil {
			s.mu.Lock()
			delete(s.crashes, appID)
			s.mu.Unlock()
			msg := fmt.Sprintf("Suspendue après %d crashs en %d minutes : relancez-la une fois le problème corrigé", len(recent), int(crashWindow.Minutes()))
			s.appEvent(appID, eventError, msg)
			s.push(ctx, suspended.NodeID)
			n := notification{Title: "« " + a.Name + " » est suspendue", Text: msg + ". Dernier crash : " + why + ".", Path: path, Color: colorError}
			s.notifyUser(a.OwnerID, n)
			s.notifyAdmins("crashes", n)
		}
		return
	}
	if tellUser {
		s.notifyUser(a.OwnerID, notification{Title: "« " + a.Name + " » a planté", Text: why + ". Docker la relance.", Path: path, Color: colorWarning})
	}
}

// crashOf tells how many times an app crashed between two reports, and how it ended last.
func crashOf(from, to *agentpb.AppStatus) (int, string) {
	why := fmt.Sprintf("code de sortie %d", to.GetExitCode())
	if to.GetOomKilled() {
		why = "manque de mémoire"
	}
	switch {
	case to.GetRestartCount() > from.GetRestartCount():
		// Docker restarted the container: whatever the state now, it stopped by itself, maybe several times
		// between two reports.
		return int(to.GetRestartCount() - from.GetRestartCount()), why
	case to.GetState() == "exited" && from.GetState() != "exited" && (to.GetExitCode() != 0 || to.GetOomKilled()):
		return 1, why
	}
	return 0, ""
}

// onNodeChange tells the admins when a node stays offline, and when it comes back.
func (s *Server) onNodeChange(nodeID int64, online bool) {
	s.mu.Lock()
	gen := s.nodeGen[nodeID] + 1
	s.nodeGen[nodeID] = gen
	wasReported := s.nodeReported[nodeID]
	if online {
		delete(s.nodeReported, nodeID)
	}
	s.mu.Unlock()

	name := func() string {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if n, err := s.store.GetNode(ctx, nodeID); err == nil {
			return n.Name
		}
		return "#" + strconv.FormatInt(nodeID, 10)
	}
	if online {
		// A node that comes back may have a new local address: the relays of Forgeyard's machine follow.
		// (The hub itself sends the node its desired state when it connects.)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			s.pushRelays(ctx, nodeID)
		}()
		if wasReported {
			s.notifyAdmins("nodes", notification{Title: "Le node « " + name() + " » est de retour", Text: "Ses apps sont de nouveau pilotées.", Path: "#/nodes", Color: colorSuccess})
		}
		return
	}
	time.AfterFunc(nodeOfflineGrace, func() {
		s.mu.Lock()
		still := s.nodeGen[nodeID] == gen
		if still {
			s.nodeReported[nodeID] = true
		}
		s.mu.Unlock()
		if still {
			s.notifyAdmins("nodes", notification{
				Title: "Le node « " + name() + " » est hors ligne", Text: "Son agent ne répond plus depuis une minute. Ses apps continuent de tourner, mais Forgeyard ne peut plus les piloter.",
				Path: "#/nodes", Color: colorError,
			})
		}
	})
}
