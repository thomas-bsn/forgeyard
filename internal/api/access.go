package api

import (
	"context"
	"database/sql"
	"errors"

	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

const errRole = "votre rôle ne permet pas cette action"

// role is what a user may do on one app or project, from nothing to everything.
type role int

const (
	roleNone role = iota
	roleViewer
	roleContributor
	roleOwner
)

// action is one thing a handler does to an app or a project. Every handler that loads one names its action,
// so access to a new route is decided in minRole and nowhere else.
type action string

const (
	actView          action = "view"
	actLogs          action = "logs"
	actUpdate        action = "update"
	actDeploy        action = "deploy"
	actProbe         action = "probe"
	actLogo          action = "logo"
	actBackupConfig  action = "backup_config"
	actBackupRun     action = "backup_run"
	actRestore       action = "restore"
	actPublish       action = "publish"
	actShell         action = "shell"
	actDelete        action = "delete"
	actMove          action = "move"
	actRevokeBinds   action = "revoke_binds"
	actMembers       action = "members"
	actEnvReveal     action = "env_reveal"
	actTransferOwner action = "transfer_owner"
	actGrantHost     action = "grant_host" // what runs on the host itself: GPUs
)

// minRole is the least role allowed to do each action. An action missing here is refused to everyone.
var minRole = map[action]role{
	actView:          roleViewer,
	actLogs:          roleViewer,
	actUpdate:        roleContributor,
	actDeploy:        roleContributor,
	actProbe:         roleContributor,
	actLogo:          roleContributor,
	actBackupConfig:  roleContributor,
	actBackupRun:     roleContributor,
	actRestore:       roleOwner,
	actPublish:       roleOwner,
	actShell:         roleOwner,
	actDelete:        roleOwner,
	actMove:          roleOwner,
	actRevokeBinds:   roleOwner,
	actMembers:       roleOwner,
	actEnvReveal:     roleOwner,
	actTransferOwner: roleOwner,
	actGrantHost:     roleOwner,
}

// resource names the app or project an action is about.
type resource struct {
	kind    string // "app" or "stack"
	id      int64
	ownerID int64
	stackID int64 // the project an app belongs to, 0 when it stands alone
}

func appResource(a db.App) resource {
	return resource{kind: "app", id: a.ID, ownerID: a.OwnerID, stackID: a.StackID}
}

func stackResource(st db.Stack) resource {
	return resource{kind: "stack", id: st.ID, ownerID: st.OwnerID}
}

// memberRole is the role stored for a user on one app or project: roleNone without a row.
func (s *Server) memberRole(ctx context.Context, kind string, id, userID int64) (role, error) {
	stored, err := s.store.GetResourceMemberRole(ctx, db.GetResourceMemberRoleParams{ResourceKind: kind, ResourceID: id, UserID: userID})
	if errors.Is(err, sql.ErrNoRows) {
		return roleNone, nil
	}
	if err != nil {
		return roleNone, err
	}
	if stored == "contributor" {
		return roleContributor, nil
	}
	return roleViewer, nil
}

// roleOn is the role a user holds on a resource. Admins and the owner hold everything. Anyone else holds the
// best of the role given on the resource and, for an app of a project, the role given on the project: the
// stored roles only raise access, they never take any away.
func (s *Server) roleOn(ctx context.Context, u db.User, res resource) (role, error) {
	if isAdmin(u) || res.ownerID == u.ID {
		return roleOwner, nil
	}
	have, err := s.memberRole(ctx, res.kind, res.id, u.ID)
	if err != nil {
		return roleNone, err
	}
	if res.kind == "app" && res.stackID != 0 {
		inherited, err := s.memberRole(ctx, "stack", res.stackID, u.ID)
		if err != nil {
			return roleNone, err
		}
		have = max(have, inherited)
	}
	return have, nil
}

// access is how a request to do an action on a resource ends: allowed, refused to someone who may not even
// know it exists (404), or refused to a member whose role is too low (403).
type access int

const (
	accessAllowed access = iota
	accessHidden
	accessForbidden
)

// check decides one action on a resource for a user.
func (s *Server) check(ctx context.Context, u db.User, res resource, act action) (access, error) {
	have, err := s.roleOn(ctx, u, res)
	if err != nil {
		return accessHidden, err
	}
	switch need, known := minRole[act]; {
	case have == roleNone:
		return accessHidden, nil
	case !known || have < need:
		return accessForbidden, nil
	}
	return accessAllowed, nil
}

// can reports whether the user may do the action on the resource.
func (s *Server) can(ctx context.Context, u db.User, res resource, act action) (bool, error) {
	got, err := s.check(ctx, u, res, act)
	return got == accessAllowed && err == nil, err
}
