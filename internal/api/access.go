package api

import (
	"context"

	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

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

// roleOn is the role a user holds on a resource. Admins hold everything.
func (s *Server) roleOn(ctx context.Context, u db.User, res resource) (role, error) {
	if isAdmin(u) || res.ownerID == u.ID {
		return roleOwner, nil
	}
	return roleNone, nil
}

// can reports whether the user may do the action on the resource.
func (s *Server) can(ctx context.Context, u db.User, res resource, act action) (bool, error) {
	need, ok := minRole[act]
	if !ok {
		return false, nil
	}
	have, err := s.roleOn(ctx, u, res)
	if err != nil {
		return false, err
	}
	return have >= need, nil
}
