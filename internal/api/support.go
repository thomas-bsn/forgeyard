package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

// Support: a user asks for help, about anything, one of their apps or the infrastructure, and the admins
// answer in the same thread. Each new request and message reaches the admins on Discord (the support
// channel when one is set), and each answer reaches the author on their own webhook.

var ticketKinds = map[string]string{"general": "Général", "app": "App", "infra": "Infra"}

type ticketResponse struct {
	ID         int64  `json:"id"`
	Kind       string `json:"kind"`
	AppID      int64  `json:"appId,omitempty"`
	AppName    string `json:"appName,omitempty"`
	Subject    string `json:"subject"`
	Status     string `json:"status"`
	Waiting    bool   `json:"waiting"` // the author wrote last: an answer is due
	AuthorID   int64  `json:"authorId"`
	AuthorName string `json:"authorName"`
	Messages   int64  `json:"messages"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}

type ticketMessage struct {
	ID         int64  `json:"id"`
	AuthorID   int64  `json:"authorId,omitempty"`
	AuthorName string `json:"authorName"`
	AvatarURL  string `json:"avatarUrl,omitempty"`
	Staff      bool   `json:"staff"` // written by an admin
	Body       string `json:"body"`
	CreatedAt  int64  `json:"createdAt"`
}

type ticketDetail struct {
	ticketResponse
	Thread []ticketMessage `json:"thread"`
}

func toTicketResponse(t db.SupportTicket, authorName string, messages int64) ticketResponse {
	return ticketResponse{
		ID: t.ID, Kind: t.Kind, AppID: t.AppID.Int64, AppName: t.AppName, Subject: t.Subject, Status: t.Status,
		Waiting: t.Waiting != 0, AuthorID: t.AuthorID, AuthorName: authorName, Messages: messages,
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}
}

func (s *Server) handleListTickets(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	var rows []db.ListTicketsRow
	var err error
	if isAdmin(user) {
		rows, err = s.store.ListTickets(r.Context())
	} else {
		var own []db.ListTicketsByAuthorRow
		own, err = s.store.ListTicketsByAuthor(r.Context(), user.ID)
		for _, o := range own {
			rows = append(rows, db.ListTicketsRow(o))
		}
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]ticketResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toTicketResponse(row.SupportTicket, row.AuthorName, row.Messages))
	}
	writeJSON(w, http.StatusOK, out)
}

type ticketRequest struct {
	Kind    string `json:"kind"`
	AppID   int64  `json:"appId"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

func validMessage(body string) (string, string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", "écrivez un message"
	}
	if utf8.RuneCountInString(body) > 5000 {
		return "", "message trop long : 5000 caractères au maximum"
	}
	return body, ""
}

func (s *Server) handleCreateTicket(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := currentUser(r)
	var in ticketRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	if _, ok := ticketKinds[in.Kind]; !ok {
		writeError(w, http.StatusBadRequest, "type de demande inconnu : général, app ou infra")
		return
	}
	subject := strings.Join(strings.Fields(in.Subject), " ")
	if n := utf8.RuneCountInString(subject); n < 3 || n > 120 {
		writeError(w, http.StatusBadRequest, "sujet : 3 à 120 caractères")
		return
	}
	body, msg := validMessage(in.Body)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	var appID sql.NullInt64
	appName := ""
	if in.Kind == "app" {
		a, err := s.store.GetApp(ctx, in.AppID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !canManage(user, a)) {
			writeError(w, http.StatusBadRequest, "choisissez une de vos apps")
			return
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		appID, appName = sql.NullInt64{Int64: a.ID, Valid: true}, a.Name
	}
	now := time.Now().Unix()
	var t db.SupportTicket
	err := s.store.InTx(ctx, func(q *db.Queries) error {
		var err error
		t, err = q.CreateTicket(ctx, db.CreateTicketParams{AuthorID: user.ID, Kind: in.Kind, AppID: appID, AppName: appName,
			Subject: subject, CreatedAt: now, UpdatedAt: now})
		if err != nil {
			return err
		}
		_, err = q.AddTicketMessage(ctx, db.AddTicketMessageParams{TicketID: t.ID, AuthorID: sql.NullInt64{Int64: user.ID, Valid: true}, Body: body, CreatedAt: now})
		return err
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	about := ticketKinds[t.Kind]
	if appName != "" {
		about += " · " + appName
	}
	s.notifyAdmins("support", notification{
		Title: "Demande d'aide : " + subject, Text: user.DisplayName + " (" + about + ")\n\n" + excerpt(body),
		Path: ticketPath(t.ID), Color: colorInfo,
	})
	s.logger.Info("support request", "ticket", t.ID, "kind", t.Kind, "by", user.DisplayName)
	s.writeTicket(w, r, http.StatusCreated, t.ID)
}

func ticketPath(id int64) string { return "#/support/" + strconv.FormatInt(id, 10) }

// excerpt keeps the start of a message for a notification.
func excerpt(body string) string {
	if utf8.RuneCountInString(body) <= 600 {
		return body
	}
	return string([]rune(body)[:600]) + "…"
}

// ticketFromPath loads the ticket of the request, which only its author and the admins may see.
func (s *Server) ticketFromPath(w http.ResponseWriter, r *http.Request) (db.GetTicketRow, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "demande introuvable")
		return db.GetTicketRow{}, false
	}
	t, err := s.store.GetTicket(r.Context(), id)
	user := currentUser(r)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && t.SupportTicket.AuthorID != user.ID && !isAdmin(user)) {
		writeError(w, http.StatusNotFound, "demande introuvable")
		return db.GetTicketRow{}, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return db.GetTicketRow{}, false
	}
	return t, true
}

func (s *Server) writeTicket(w http.ResponseWriter, r *http.Request, status int, id int64) {
	ctx := r.Context()
	t, err := s.store.GetTicket(ctx, id)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	msgs, err := s.store.ListTicketMessages(ctx, id)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := ticketDetail{ticketResponse: toTicketResponse(t.SupportTicket, t.AuthorName, int64(len(msgs))), Thread: []ticketMessage{}}
	avatars := map[int64]string{}
	for _, m := range msgs {
		tm := ticketMessage{ID: m.ID, AuthorName: m.AuthorName, Body: m.Body, CreatedAt: m.CreatedAt,
			Staff: m.AuthorRole == "admin" || m.AuthorRole == "superadmin"}
		if !m.AuthorID.Valid {
			tm.AuthorName = "Compte supprimé"
		} else {
			tm.AuthorID = m.AuthorID.Int64
			if _, ok := avatars[tm.AuthorID]; !ok {
				if u, err := s.store.GetUserByID(ctx, tm.AuthorID); err == nil {
					avatars[tm.AuthorID] = avatarURL(u)
				}
			}
			tm.AvatarURL = avatars[tm.AuthorID]
		}
		out.Thread = append(out.Thread, tm)
	}
	writeJSON(w, status, out)
}

func (s *Server) handleGetTicket(w http.ResponseWriter, r *http.Request) {
	if t, ok := s.ticketFromPath(w, r); ok {
		s.writeTicket(w, r, http.StatusOK, t.SupportTicket.ID)
	}
}

// handleTicketMessage adds a message to a thread; it reopens a closed request.
func (s *Server) handleTicketMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t, ok := s.ticketFromPath(w, r)
	if !ok {
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	body, msg := validMessage(in.Body)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	user := currentUser(r)
	byAuthor := user.ID == t.SupportTicket.AuthorID
	now := time.Now().Unix()
	err := s.store.InTx(ctx, func(q *db.Queries) error {
		if _, err := q.AddTicketMessage(ctx, db.AddTicketMessageParams{TicketID: t.SupportTicket.ID, AuthorID: sql.NullInt64{Int64: user.ID, Valid: true}, Body: body, CreatedAt: now}); err != nil {
			return err
		}
		return q.TouchTicket(ctx, db.TouchTicketParams{Waiting: boolInt(byAuthor), UpdatedAt: now, ID: t.SupportTicket.ID})
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	n := notification{Path: ticketPath(t.SupportTicket.ID), Color: colorInfo}
	if byAuthor {
		n.Title, n.Text = "Nouveau message : "+t.SupportTicket.Subject, user.DisplayName+"\n\n"+excerpt(body)
		s.notifyAdmins("support", n)
	} else {
		n.Title, n.Text, n.Color = "Réponse du support : "+t.SupportTicket.Subject, user.DisplayName+"\n\n"+excerpt(body), colorSuccess
		s.notifyUser(t.SupportTicket.AuthorID, n)
	}
	s.writeTicket(w, r, http.StatusOK, t.SupportTicket.ID)
}

func (s *Server) handleTicketStatus(w http.ResponseWriter, r *http.Request) {
	t, ok := s.ticketFromPath(w, r)
	if !ok {
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Status != "open" && in.Status != "closed" {
		writeError(w, http.StatusBadRequest, "statut : open ou closed")
		return
	}
	if err := s.store.SetTicketStatus(r.Context(), db.SetTicketStatusParams{Status: in.Status, UpdatedAt: time.Now().Unix(), ID: t.SupportTicket.ID}); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.writeTicket(w, r, http.StatusOK, t.SupportTicket.ID)
}
