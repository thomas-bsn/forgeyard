package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/discord"
	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

// The instance's Discord bot keeps the members' Discord name, picture and banner up to date: otherwise they
// only change when the member signs in with Discord again, and a picture changed on Discord shows as the
// initial (Discord deletes the old one). The bot is the token of the Bot tab of the Discord application
// that signs people in; it needs no permission nor server to read a profile.

const (
	settingDiscordBotToken = "discord_bot_token" // encrypted
	settingDiscordBotName  = "discord_bot_name"

	// Every profile is read again once a day, and right away when the bot is set.
	discordRefreshEvery = 24 * time.Hour
)

// discordBotToken is the bot's token, "" without a bot.
func (s *Server) discordBotToken(ctx context.Context) (string, error) {
	sealed, err := s.setting(ctx, s.store.Queries, settingDiscordBotToken)
	if err != nil || sealed == "" {
		return "", err
	}
	return s.secrets.Decrypt(sealed, settingDiscordBotToken)
}

// refreshDiscordUser reads a member's Discord profile with the bot and keeps it.
func (s *Server) refreshDiscordUser(ctx context.Context, token string, u db.User) error {
	d, err := s.discord.Bot(ctx, token, u.DiscordID.String)
	if err != nil {
		return err
	}
	return s.store.UpdateDiscordProfile(ctx, db.UpdateDiscordProfileParams{
		DiscordName: d.DisplayName(), DiscordAvatar: d.Avatar, DiscordBanner: d.Banner, DiscordAccent: d.Accent(),
		Email: sql.NullString{}, ID: u.ID, // the bot does not see emails: the one kept stays
	})
}

// refreshDiscordProfiles reads every Discord member's profile again, a few per second.
func (s *Server) refreshDiscordProfiles(ctx context.Context) {
	token, err := s.discordBotToken(ctx)
	if err != nil || token == "" {
		return
	}
	users, err := s.store.ListUsers(ctx)
	if err != nil {
		s.logger.Warn("listing users for the Discord bot failed", "err", err)
		return
	}
	done := 0
	for _, row := range users {
		u := db.User{ID: row.ID, DiscordID: row.DiscordID}
		if !u.DiscordID.Valid || u.DiscordID.String == "" {
			continue
		}
		if err := s.refreshDiscordUser(ctx, token, u); err != nil {
			if errors.Is(err, discord.ErrBotToken) {
				s.logger.Warn("the Discord bot's token is refused: profiles are not refreshed")
				return
			}
			s.logger.Warn("refreshing a Discord profile failed", "user", row.DisplayName, "err", err)
			continue
		}
		done++
		select {
		case <-ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
	s.logger.Info("Discord profiles refreshed", "users", done)
}

// DiscordProfiles refreshes the members' Discord profiles once a day, until ctx ends.
func (s *Server) DiscordProfiles(ctx context.Context) {
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		s.refreshDiscordProfiles(ctx)
		timer.Reset(discordRefreshEvery)
	}
}

type discordBotResponse struct {
	Set  bool   `json:"set"`
	Name string `json:"name,omitempty"` // the bot's Discord name
}

func (s *Server) handleGetDiscordBot(w http.ResponseWriter, r *http.Request) {
	token, err := s.discordBotToken(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	name, err := s.setting(r.Context(), s.store.Queries, settingDiscordBotName)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, discordBotResponse{Set: token != "", Name: name})
}

// handlePutDiscordBot checks a bot's token with Discord, keeps it, and refreshes every profile.
func (s *Server) handlePutDiscordBot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body struct {
		Token string `json:"token"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	token := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(body.Token), "Bot "))
	if token == "" {
		writeError(w, http.StatusBadRequest, "collez le token de l'onglet Bot de l'application Discord")
		return
	}
	bot, err := s.discord.Bot(ctx, token, "@me")
	if errors.Is(err, discord.ErrBotToken) {
		writeError(w, http.StatusBadRequest, "Discord refuse ce token : copiez-le de nouveau (Bot › Reset Token)")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "Discord ne répond pas : "+err.Error())
		return
	}
	sealed, err := s.secrets.Encrypt(token, settingDiscordBotToken)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.store.InTx(ctx, func(q *db.Queries) error {
		return storeSettings(ctx, q, map[string]string{settingDiscordBotToken: sealed, settingDiscordBotName: bot.DisplayName()})
	}); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.logger.Info("Discord bot set", "bot", bot.DisplayName(), "by", currentUser(r).DisplayName)
	go s.refreshDiscordProfiles(context.WithoutCancel(ctx))
	writeJSON(w, http.StatusOK, discordBotResponse{Set: true, Name: bot.DisplayName()})
}

func (s *Server) handleDeleteDiscordBot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.store.InTx(ctx, func(q *db.Queries) error {
		if err := q.DeleteSetting(ctx, settingDiscordBotToken); err != nil {
			return err
		}
		return q.DeleteSetting(ctx, settingDiscordBotName)
	}); err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, discordBotResponse{})
}

// handleRefreshMyDiscord reads the current user's Discord profile again with the bot. Without a bot it
// answers 409: the page then goes through Discord sign-in, which reads it too.
func (s *Server) handleRefreshMyDiscord(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := currentUser(r)
	if !u.DiscordID.Valid || u.DiscordID.String == "" {
		writeError(w, http.StatusBadRequest, "ce compte n'est pas relié à Discord")
		return
	}
	token, err := s.discordBotToken(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if token == "" {
		writeError(w, http.StatusConflict, "pas de bot Discord : reconnectez-vous avec Discord")
		return
	}
	if err := s.refreshDiscordUser(ctx, token, u); err != nil {
		writeError(w, http.StatusBadGateway, "Discord : "+err.Error())
		return
	}
	u, err = s.store.GetUserByID(ctx, u.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toUserResponse(u))
}
