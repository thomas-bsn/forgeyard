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

	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

func TestCanRoleMatrix(t *testing.T) {
	s := &Server{}
	owner := db.User{ID: 1, Role: "user"}
	stranger := db.User{ID: 2, Role: "user"}
	admin := db.User{ID: 3, Role: "admin"}
	superadmin := db.User{ID: 4, Role: "superadmin"}
	res := resource{kind: "app", id: 10, ownerID: owner.ID}

	for act := range minRole {
		for _, c := range []struct {
			who  string
			user db.User
			want bool
		}{
			{"owner", owner, true},
			{"admin", admin, true},
			{"superadmin", superadmin, true},
			{"stranger", stranger, false},
		} {
			got, err := s.can(context.Background(), c.user, res, act)
			if err != nil {
				t.Fatalf("%s %s: %v", c.who, act, err)
			}
			if got != c.want {
				t.Errorf("%s may %s = %v, want %v", c.who, act, got, c.want)
			}
		}
	}
}

func TestCanRefusesUnknownAction(t *testing.T) {
	s := &Server{}
	admin := db.User{ID: 1, Role: "superadmin"}
	if ok, _ := s.can(context.Background(), admin, resource{kind: "app", ownerID: 1}, action("nope")); ok {
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
