package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

// memberEntry is one person with access to an app or a project.
type memberEntry struct {
	UserID      int64  `json:"userId"`
	DisplayName string `json:"displayName"`
	AvatarURL   string `json:"avatarUrl,omitempty"`
	Role        string `json:"role"`          // owner, contributor or viewer
	Via         string `json:"via,omitempty"` // "project" when an app gets the role from its project
}

// membersResponse lists who may use a resource. Everyone with access sees it, only the owner changes it.
type membersResponse struct {
	Owner   memberEntry   `json:"owner"`
	Members []memberEntry `json:"members"`
	Role    string        `json:"role"` // the caller's own role
}

type memberInput struct {
	Role string `json:"role"`
}

type transferInput struct {
	UserID int64 `json:"userId"`
}

func roleName(r role) string {
	switch r {
	case roleOwner:
		return "owner"
	case roleContributor:
		return "contributor"
	case roleViewer:
		return "viewer"
	}
	return ""
}

// listMembers answers the members of a resource, with the project's members first for an app of a project.
func (s *Server) listMembers(w http.ResponseWriter, r *http.Request, res resource) {
	ctx := r.Context()
	owner, err := s.store.GetUserByID(ctx, res.ownerID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	mine, err := s.roleOn(ctx, currentUser(r), res)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := membersResponse{
		Owner:   memberEntry{UserID: owner.ID, DisplayName: owner.DisplayName, AvatarURL: avatarURL(owner), Role: "owner"},
		Members: []memberEntry{},
		Role:    roleName(mine),
	}
	seen := map[int64]int{}
	add := func(kind string, id int64, via string) error {
		rows, err := s.store.ListResourceMembers(ctx, db.ListResourceMembersParams{ResourceKind: kind, ResourceID: id})
		if err != nil {
			return err
		}
		for _, m := range rows {
			e := memberEntry{UserID: m.User.ID, DisplayName: m.User.DisplayName, AvatarURL: avatarURL(m.User), Role: m.Role, Via: via}
			// A role given on the app itself and one inherited: the better one shows.
			if i, dup := seen[m.User.ID]; dup {
				if e.Role == "contributor" && out.Members[i].Role == "viewer" {
					out.Members[i] = e
				}
				continue
			}
			seen[m.User.ID] = len(out.Members)
			out.Members = append(out.Members, e)
		}
		return nil
	}
	if err := add(res.kind, res.id, ""); err != nil {
		s.internalError(w, r, err)
		return
	}
	if res.kind == "app" && res.stackID != 0 {
		if err := add("stack", res.stackID, "project"); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// eventsFor records an event on the apps a resource stands for: the app itself, or every service of a project.
func (s *Server) eventsFor(r *http.Request, res resource, message string) {
	ctx := r.Context()
	ids := []int64{res.id}
	if res.kind == "stack" {
		ids = nil
		apps, err := s.store.ListStackApps(ctx, res.id)
		if err != nil {
			s.logger.Warn("listing the services to record an event failed", "stack_id", res.id, "err", err)
			return
		}
		for _, a := range apps {
			ids = append(ids, a.ID)
		}
	}
	for _, id := range ids {
		s.appEventBy(id, currentUser(r).ID, eventInfo, message)
	}
}

// setMember gives a user a role on a resource, or changes it.
func (s *Server) setMember(w http.ResponseWriter, r *http.Request, res resource) {
	ctx := r.Context()
	userID, err := strconv.ParseInt(r.PathValue("user"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "membre introuvable")
		return
	}
	var in memberInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Role != "viewer" && in.Role != "contributor" {
		writeError(w, http.StatusBadRequest, "le rôle est viewer ou contributor")
		return
	}
	target, err := s.store.GetUserByID(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && target.Disabled != 0) {
		writeError(w, http.StatusNotFound, "utilisateur introuvable")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if target.ID == res.ownerID {
		writeError(w, http.StatusConflict, "le propriétaire a déjà tous les droits")
		return
	}
	err = s.store.SetResourceMember(ctx, db.SetResourceMemberParams{
		ResourceKind: res.kind, ResourceID: res.id, UserID: target.ID, Role: in.Role,
		AddedBy: sql.NullInt64{Int64: currentUser(r).ID, Valid: true}, CreatedAt: time.Now().Unix(),
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.eventsFor(r, res, currentUser(r).DisplayName+" donne le rôle "+in.Role+" à "+target.DisplayName)
	s.listMembers(w, r, res)
}

// removeMember takes a user's role away from a resource.
func (s *Server) removeMember(w http.ResponseWriter, r *http.Request, res resource) {
	userID, err := strconv.ParseInt(r.PathValue("user"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "membre introuvable")
		return
	}
	n, err := s.store.RemoveResourceMember(r.Context(), db.RemoveResourceMemberParams{ResourceKind: res.kind, ResourceID: res.id, UserID: userID})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, "membre introuvable")
		return
	}
	name := "un utilisateur"
	if u, err := s.store.GetUserByID(r.Context(), userID); err == nil {
		name = u.DisplayName
	}
	s.eventsFor(r, res, currentUser(r).DisplayName+" retire l'accès de "+name)
	s.listMembers(w, r, res)
}

func (s *Server) handleListAppMembers(w http.ResponseWriter, r *http.Request) {
	if a, ok := s.appFromPath(w, r, actView); ok {
		s.listMembers(w, r, appResource(a))
	}
}

func (s *Server) handleSetAppMember(w http.ResponseWriter, r *http.Request) {
	if a, ok := s.appFromPath(w, r, actMembers); ok {
		s.setMember(w, r, appResource(a))
	}
}

func (s *Server) handleRemoveAppMember(w http.ResponseWriter, r *http.Request) {
	if a, ok := s.appFromPath(w, r, actMembers); ok {
		s.removeMember(w, r, appResource(a))
	}
}

func (s *Server) handleListStackMembers(w http.ResponseWriter, r *http.Request) {
	if st, ok := s.stackFromPath(w, r, actView); ok {
		s.listMembers(w, r, stackResource(st))
	}
}

func (s *Server) handleSetStackMember(w http.ResponseWriter, r *http.Request) {
	if st, ok := s.stackFromPath(w, r, actMembers); ok {
		s.setMember(w, r, stackResource(st))
	}
}

func (s *Server) handleRemoveStackMember(w http.ResponseWriter, r *http.Request) {
	if st, ok := s.stackFromPath(w, r, actMembers); ok {
		s.removeMember(w, r, stackResource(st))
	}
}

// transferTarget reads and checks the user a resource is handed to.
func (s *Server) transferTarget(w http.ResponseWriter, r *http.Request, res resource) (db.User, bool) {
	var in transferInput
	if !decodeJSON(w, r, &in) {
		return db.User{}, false
	}
	target, err := s.store.GetUserByID(r.Context(), in.UserID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && target.Disabled != 0) {
		writeError(w, http.StatusNotFound, "utilisateur introuvable")
		return db.User{}, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return db.User{}, false
	}
	if target.ID == res.ownerID {
		writeError(w, http.StatusConflict, "cet utilisateur est déjà le propriétaire")
		return db.User{}, false
	}
	return target, true
}

// handleTransferApp gives a standalone app to another user. The former owner keeps no access: they can be
// given a role again afterwards.
func (s *Server) handleTransferApp(w http.ResponseWriter, r *http.Request) {
	a, ok := s.appFromPath(w, r, actTransferOwner)
	if !ok {
		return
	}
	if a.StackID != 0 {
		writeError(w, http.StatusConflict, errStackApp)
		return
	}
	target, ok := s.transferTarget(w, r, appResource(a))
	if !ok {
		return
	}
	err := s.store.InTx(r.Context(), func(q *db.Queries) error {
		if err := q.SetAppOwner(r.Context(), db.SetAppOwnerParams{OwnerID: target.ID, UpdatedAt: time.Now().Unix(), ID: a.ID}); err != nil {
			return err
		}
		_, err := q.RemoveResourceMember(r.Context(), db.RemoveResourceMemberParams{ResourceKind: "app", ResourceID: a.ID, UserID: target.ID})
		return err
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.appEventBy(a.ID, currentUser(r).ID, eventWarning, currentUser(r).DisplayName+" transfère l'app à "+target.DisplayName)
	s.push(r.Context(), a.NodeID)
	a.OwnerID = target.ID
	s.writeApp(w, r, http.StatusOK, a, false)
}

// handleTransferStack gives a project, and its services, to another user.
func (s *Server) handleTransferStack(w http.ResponseWriter, r *http.Request) {
	st, ok := s.stackFromPath(w, r, actTransferOwner)
	if !ok {
		return
	}
	target, ok := s.transferTarget(w, r, stackResource(st))
	if !ok {
		return
	}
	err := s.store.InTx(r.Context(), func(q *db.Queries) error {
		now := time.Now().Unix()
		if err := q.SetStackOwner(r.Context(), db.SetStackOwnerParams{OwnerID: target.ID, UpdatedAt: now, ID: st.ID}); err != nil {
			return err
		}
		if err := q.SetStackAppsOwner(r.Context(), db.SetStackAppsOwnerParams{OwnerID: target.ID, UpdatedAt: now, StackID: st.ID}); err != nil {
			return err
		}
		_, err := q.RemoveResourceMember(r.Context(), db.RemoveResourceMemberParams{ResourceKind: "stack", ResourceID: st.ID, UserID: target.ID})
		return err
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.eventsFor(r, stackResource(st), currentUser(r).DisplayName+" transfère le projet à "+target.DisplayName)
	st.OwnerID = target.ID
	s.listMembers(w, r, stackResource(st))
}

// userMemberships are the roles given to one user, to label the apps and projects of a list.
type userMemberships struct {
	userID int64
	admin  bool // admins hold everything: no role to show
	roles  map[string]role
}

func (s *Server) memberships(ctx context.Context, u db.User) (userMemberships, error) {
	m := userMemberships{userID: u.ID, admin: isAdmin(u), roles: map[string]role{}}
	rows, err := s.store.ListMembershipsOfUser(ctx, u.ID)
	for _, row := range rows {
		given := roleViewer
		if row.Role == "contributor" {
			given = roleContributor
		}
		m.roles[row.ResourceKind+":"+strconv.FormatInt(row.ResourceID, 10)] = given
	}
	return m, err
}

// of gives the role the user was given on a resource, and "project" when an app has it from its project.
// Nothing for the resources the user owns.
func (m userMemberships) of(res resource) (given, via string) {
	if m.admin || res.ownerID == m.userID {
		return "", ""
	}
	direct := m.roles[res.kind+":"+strconv.FormatInt(res.id, 10)]
	var inherited role
	if res.kind == "app" && res.stackID != 0 {
		inherited = m.roles["stack:"+strconv.FormatInt(res.stackID, 10)]
	}
	if inherited > direct {
		return roleName(inherited), "project"
	}
	return roleName(direct), ""
}
