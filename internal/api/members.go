package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

// Members are the accounts every signed-in user can see: a public profile with what its owner chose to
// show, never an app's configuration, logs or environment.

type memberSummary struct {
	ID             int64  `json:"id"`
	DisplayName    string `json:"displayName"`
	Role           string `json:"role"`
	AvatarURL      string `json:"avatarUrl,omitempty"`
	Bio            string `json:"bio"`
	PublicApps     int64  `json:"publicApps"`     // the apps shown on their own, not a project's service
	PublicProjects int64  `json:"publicProjects"` // the projects showing at least one of their apps
	CreatedAt      int64  `json:"createdAt"`
}

func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.ListMembers(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]memberSummary, 0, len(rows))
	for _, m := range rows {
		u := memberUser(m)
		apps, projects := m.PublicApps, m.PublicProjects
		if u.ShowApps == 0 {
			apps, projects = 0, 0
		}
		out = append(out, memberSummary{
			ID: u.ID, DisplayName: u.DisplayName, Role: u.Role, AvatarURL: avatarURL(u), Bio: u.Bio,
			PublicApps: apps, PublicProjects: projects, CreatedAt: u.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func memberUser(m db.ListMembersRow) db.User {
	return db.User{
		ID: m.ID, DisplayName: m.DisplayName, Role: m.Role, DiscordID: m.DiscordID, DiscordAvatar: m.DiscordAvatar,
		AvatarUpdatedAt: m.AvatarUpdatedAt, Bio: m.Bio, ShowApps: m.ShowApps, CreatedAt: m.CreatedAt,
	}
}

type memberApp struct {
	ID      int64   `json:"id"`
	Name    string  `json:"name"`
	URL     string  `json:"url,omitempty"`
	State   string  `json:"state"`
	Logo    appLogo `json:"logo"`
	Kind    string  `json:"kind,omitempty"`    // web or sandbox for a standalone app
	Service string  `json:"service,omitempty"` // its name in its project's compose file
}

// memberProject is a project on a member's profile: it shows when at least one of its apps is public, and
// only its public apps are listed inside.
type memberProject struct {
	ID       int64       `json:"id"`
	Name     string      `json:"name"`
	URL      string      `json:"url,omitempty"` // the address of its exposed service, if it has one
	State    string      `json:"state"`
	Logo     stackLogo   `json:"logo"`
	Services []memberApp `json:"services"`
}

type memberActivity struct {
	At      int64  `json:"at"`
	Kind    string `json:"kind"`
	App     string `json:"app"`
	Message string `json:"message"`
}

type memberProfile struct {
	memberSummary
	BannerURL   string           `json:"bannerUrl,omitempty"`
	AccentColor string           `json:"accentColor,omitempty"`
	DiscordID   string           `json:"discordId,omitempty"`
	Email       string           `json:"email,omitempty"`
	ShowApps    bool             `json:"showApps"`
	Apps        []memberApp      `json:"apps"`
	Projects    []memberProject  `json:"projects"`
	Activity    []memberActivity `json:"activity"`
}

// handleGetMember returns a member's public profile: the apps they show on their own, their projects (with
// the apps they show in each), and what recently happened to them.
func (s *Server) handleGetMember(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "membre introuvable")
		return
	}
	u, err := s.store.GetUserByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && u.Disabled != 0) {
		writeError(w, http.StatusNotFound, "membre introuvable")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	p := memberProfile{
		memberSummary: memberSummary{ID: u.ID, DisplayName: u.DisplayName, Role: u.Role, AvatarURL: avatarURL(u), Bio: u.Bio, CreatedAt: u.CreatedAt},
		BannerURL:     bannerURL(u), AccentColor: accentColor(u), DiscordID: u.DiscordID.String, ShowApps: u.ShowApps != 0,
		Apps: []memberApp{}, Projects: []memberProject{}, Activity: []memberActivity{},
	}
	if u.ShowEmail != 0 {
		p.Email = u.Email.String
	}
	if u.ShowApps == 0 {
		writeJSON(w, http.StatusOK, p)
		return
	}
	apps, err := s.store.ListPublicAppsByOwner(ctx, u.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	c, err := s.loadDNSConfig(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	// An app of a project is shown inside its project, never as an app on its own.
	byStack := map[int64][]appResponse{}
	for _, a := range apps {
		resp := s.toAppResponse(a, u.DisplayName, u.AppsSuspended != 0, "", c, nil, "")
		if a.StackID == 0 {
			p.Apps = append(p.Apps, memberApp{ID: a.ID, Name: a.Name, URL: resp.URL, State: resp.State, Logo: resp.Logo, Kind: a.Kind})
			continue
		}
		byStack[a.StackID] = append(byStack[a.StackID], resp)
	}
	// Projects in name order; one with no public app at all is left out.
	stacks, err := s.store.ListStacksByOwner(ctx, u.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	for _, st := range stacks {
		services, ok := byStack[st.Stack.ID]
		if !ok {
			continue
		}
		proj := memberProject{ID: st.Stack.ID, Name: st.Stack.Name, Logo: toStackLogo(st.Stack), Services: []memberApp{}}
		for _, svc := range services {
			if svc.Kind == kindWeb && svc.URL != "" {
				proj.URL = svc.URL
			}
			proj.Services = append(proj.Services, memberApp{
				ID: svc.ID, Name: svc.Name, URL: svc.URL, State: svc.State, Logo: svc.Logo, Service: svc.Service,
			})
		}
		proj.State = stackState(services)
		p.Projects = append(p.Projects, proj)
	}
	p.PublicApps = int64(len(p.Apps))
	p.PublicProjects = int64(len(p.Projects))
	if events, err := s.store.ListMemberActivity(ctx, db.ListMemberActivityParams{OwnerID: u.ID, Limit: 10}); err == nil {
		for _, e := range events {
			p.Activity = append(p.Activity, memberActivity{At: e.At, Kind: e.Kind, App: e.AppName, Message: e.Message})
		}
	}
	writeJSON(w, http.StatusOK, p)
}

// handleSetAppPublic shows or hides an app on its owner's public profile.
func (s *Server) handleSetAppPublic(w http.ResponseWriter, r *http.Request) {
	a, ok := s.appFromPath(w, r, actPublish)
	if !ok {
		return
	}
	var body struct {
		Public bool `json:"public"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := s.store.SetAppPublic(r.Context(), db.SetAppPublicParams{Public: boolInt(body.Public), ID: a.ID}); err != nil {
		s.internalError(w, r, err)
		return
	}
	a.Public = boolInt(body.Public)
	s.writeApp(w, r, http.StatusOK, a, false)
}
