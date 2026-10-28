package api

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

// testUser creates a local user with the role and returns it.
func testUser(t *testing.T, server *Server, username, role string) db.User {
	t.Helper()
	u, err := server.store.CreateLocalUser(context.Background(), db.CreateLocalUserParams{
		Username: nullString(username), PasswordHash: nullString("x"), DisplayName: username, Role: role, CreatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func giveRole(t *testing.T, server *Server, kind string, id int64, u db.User, role string) {
	t.Helper()
	if err := server.store.SetResourceMember(context.Background(), db.SetResourceMemberParams{
		ResourceKind: kind, ResourceID: id, UserID: u.ID, Role: role, CreatedAt: time.Now().Unix(),
	}); err != nil {
		t.Fatal(err)
	}
}

var (
	viewerActions      = []action{actView, actLogs}
	contributorActions = []action{actUpdate, actDeploy, actProbe, actLogo, actBackupConfig, actBackupRun}
	ownerActions       = []action{actRestore, actPublish, actShell, actDelete, actMove, actRevokeBinds, actMembers, actEnvReveal, actTransferOwner, actGrantHost}
)

func TestCanRoleMatrix(t *testing.T) {
	_, s := newTestServerWithHandle(t)
	owner := testUser(t, s, "owner", "user")
	viewer := testUser(t, s, "viewer", "user")
	contributor := testUser(t, s, "contributor", "user")
	stranger := testUser(t, s, "stranger", "user")
	admin := testUser(t, s, "admin", "admin")
	res := resource{kind: "app", id: 10, ownerID: owner.ID}
	giveRole(t, s, "app", res.id, viewer, "viewer")
	giveRole(t, s, "app", res.id, contributor, "contributor")

	all := slices.Concat(viewerActions, contributorActions, ownerActions)
	if len(all) != len(minRole) {
		t.Fatalf("the matrix names %d actions, minRole has %d: a new action needs a line here", len(all), len(minRole))
	}
	for _, c := range []struct {
		who     string
		user    db.User
		allowed []action
	}{
		{"owner", owner, all},
		{"admin", admin, all},
		{"contributor", contributor, slices.Concat(viewerActions, contributorActions)},
		{"viewer", viewer, viewerActions},
		{"stranger", stranger, nil},
	} {
		for _, act := range all {
			got, err := s.can(context.Background(), c.user, res, act)
			if err != nil {
				t.Fatalf("%s %s: %v", c.who, act, err)
			}
			if want := slices.Contains(c.allowed, act); got != want {
				t.Errorf("%s may %s = %v, want %v", c.who, act, got, want)
			}
		}
	}

	// A member refused an action learns it exists and lacks the role (403); a stranger learns nothing (404).
	if got, _ := s.check(context.Background(), viewer, res, actDeploy); got != accessForbidden {
		t.Errorf("viewer deploying: %v, want forbidden", got)
	}
	if got, _ := s.check(context.Background(), stranger, res, actView); got != accessHidden {
		t.Errorf("stranger viewing: %v, want hidden", got)
	}
}

func TestRoleOnInheritsFromTheProject(t *testing.T) {
	_, s := newTestServerWithHandle(t)
	owner := testUser(t, s, "owner", "user")
	pat := testUser(t, s, "pat", "user")
	lou := testUser(t, s, "lou", "user")
	service := resource{kind: "app", id: 5, ownerID: owner.ID, stackID: 7}
	standalone := resource{kind: "app", id: 6, ownerID: owner.ID}
	giveRole(t, s, "stack", 7, pat, "contributor")
	// Lou is a viewer of the project but a contributor of this one service: the better role wins.
	giveRole(t, s, "stack", 7, lou, "viewer")
	giveRole(t, s, "app", 5, lou, "contributor")

	ctx := context.Background()
	for _, c := range []struct {
		who  string
		user db.User
		res  resource
		want role
	}{
		{"pat on a service", pat, service, roleContributor},
		{"pat on a standalone app", pat, standalone, roleNone},
		{"lou on the service", lou, service, roleContributor},
	} {
		got, err := s.roleOn(ctx, c.user, c.res)
		if err != nil || got != c.want {
			t.Errorf("%s: %v (%v), want %v", c.who, got, err, c.want)
		}
	}
}

func TestCanRefusesUnknownAction(t *testing.T) {
	_, s := newTestServerWithHandle(t)
	admin := testUser(t, s, "boss", "superadmin")
	if ok, _ := s.can(context.Background(), admin, resource{kind: "app", ownerID: admin.ID}, action("nope")); ok {
		t.Error("an action missing from minRole must be refused, even to a superadmin")
	}
}

// Routes that name an app or a project in their path, and are allowed to load it without an action:
// the pictures any signed-in user can fetch, and the project page, which any member may open and which
// decides what to show from can(actView).
var unguardedResourceRoutes = map[string]bool{
	"handleGetAppLogo":   true,
	"handleGetStackLogo": true,
	"handleGetStack":     true,
}

// TestEveryResourceRouteNamesItsAction fails when a route on /apps/{id} or /stacks/{id} loads the resource
// without going through appFromPath, stackFromPath or policyFromPath, which is how a new handler would skip
// the access check.
func TestEveryResourceRouteNamesItsAction(t *testing.T) {
	routerSrc, err := os.ReadFile("router.go")
	if err != nil {
		t.Fatal(err)
	}
	route := regexp.MustCompile(`mux\.HandleFunc\("[A-Z]+ (/api/(?:admin/)?(?:apps|stacks)/\{id\}[^"]*)", s\.\w+\(s\.(\w+)\)\)`)
	handlers := map[string]string{}
	for _, m := range route.FindAllStringSubmatch(string(routerSrc), -1) {
		handlers[m[2]] = m[1]
	}
	if len(handlers) < 20 {
		t.Fatalf("found only %d app or project routes in router.go, the pattern has drifted", len(handlers))
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	bodies := map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Body != nil {
				bodies[fn.Name.Name] = string(src[fset.Position(fn.Body.Pos()).Offset:fset.Position(fn.Body.End()).Offset])
			}
		}
	}

	guards := []string{"s.appFromPath(", "s.stackFromPath(", "s.policyFromPath("}
	for name, path := range handlers {
		if unguardedResourceRoutes[name] {
			continue
		}
		body, ok := bodies[name]
		if !ok {
			t.Errorf("%s (%s): handler not found", name, path)
			continue
		}
		if !slices.ContainsFunc(guards, func(g string) bool { return strings.Contains(body, g) }) {
			t.Errorf("%s (%s) does not load its resource through appFromPath, stackFromPath or policyFromPath", name, path)
		}
	}
}
