package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// parameterScope is a scope that gates a query parameter, not a route. The
// route's own middleware admits any credential holding tickets:read; the
// handler then refuses the parameter unless the scope is held too. No router
// walk can see that, so TestScopesForRole_MatchTheRouter would call the scope
// unused. This list is the named exception: it is read by that test, and it is
// short on purpose. A scope belongs here only if it truly gates a parameter.
type parameterScope struct {
	scope string
	// roles whose keys may hold it. Must equal what auth.ScopesForRole offers.
	roles []user.Role
	// route is the walkRouter key of the route that carries the parameter.
	route string
	param string
	// handlerTest names the test (any *_test.go in this directory, either
	// package) that proves the handler refuses the parameter without the scope.
	// Its body must name the scope.
	handlerTest string
	// pending is set only while handlerTest does not exist yet, and names who
	// adds it. TestParameterScopes_AreCheckedByAHandlerTest skips such an entry
	// while the test is absent and fails the moment the test appears, so the
	// marker cannot outlive the thing it excuses.
	pending string
}

var parameterScopes = []parameterScope{
	{
		scope:       "requester_search:read",
		roles:       []user.Role{user.RoleAdmin, user.RoleStaff},
		route:       "GET /api/v1/tickets",
		param:       "requester",
		handlerTest: "TestListTickets_RequesterSearchNeedsTheScope",
		pending:     "the REST handler change for the ticket filters adds the handler check and this test",
	},
}

// parameterScopesFor is what TestScopesForRole_MatchTheRouter adds to the
// router-derived set for role.
func parameterScopesFor(role user.Role) []string {
	var out []string
	for _, p := range parameterScopes {
		if slices.Contains(p.roles, role) {
			out = append(out, p.scope)
		}
	}
	return out
}

// Each entry must be a real scope, on a route that exists, and must not be one
// the router already derives (then it gates a route and does not belong here).
func TestParameterScopes_AreWhatTheyClaim(t *testing.T) {
	routes := walkRouter(t, routeOnlyServer(map[string]probeKey{}))
	for _, p := range parameterScopes {
		t.Run(p.scope, func(t *testing.T) {
			if err := auth.ValidateScopes([]string{p.scope}); err != nil {
				t.Fatal(err)
			}
			if _, ok := routes[p.route]; !ok {
				t.Errorf("%s: route %q is not registered", p.scope, p.route)
			}
			for _, role := range []user.Role{user.RoleAdmin, user.RoleStaff} {
				if slices.Contains(scopesTheRouterGives(t, role), p.scope) {
					t.Errorf("%s is derived from the router for %s, so it gates a route: drop it from parameterScopes", p.scope, role)
				}
				if slices.Contains(p.roles, role) != slices.Contains(offeredTo(role), p.scope) {
					t.Errorf("%s: roles says %v but auth.ScopesForRole(%s) disagrees", p.scope, p.roles, role)
				}
			}
		})
	}
}

// functionBody returns the source of the func named name declared in any
// *_test.go file in dir, and whether one exists.
func functionBody(t *testing.T, dir, name string) (string, bool) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if ok && fn.Recv == nil && fn.Name.Name == name && fn.Body != nil {
				return string(src[fset.Position(fn.Pos()).Offset:fset.Position(fn.End()).Offset]), true
			}
		}
	}
	return "", false
}

// Every parameter scope must be exercised by a handler test: the router test
// cannot see it, so without this a handler could forget the check and nothing
// would fail.
func TestParameterScopes_AreCheckedByAHandlerTest(t *testing.T) {
	for _, p := range parameterScopes {
		t.Run(p.scope, func(t *testing.T) {
			body, found := functionBody(t, ".", p.handlerTest)
			switch {
			case !found && p.pending != "":
				t.Skipf("pending: %s", p.pending)
			case !found:
				t.Fatalf("no test named %s: %s has no handler test", p.handlerTest, p.scope)
			case p.pending != "":
				t.Fatalf("%s exists now: delete the pending marker on the %s entry", p.handlerTest, p.scope)
			case !strings.Contains(body, p.scope):
				t.Errorf("%s never names %s, so it cannot be checking it", p.handlerTest, p.scope)
			}
		})
	}
}

// The guard above is only worth having if it can fail.
func TestFunctionBody_FindsWhatItShould(t *testing.T) {
	dir := t.TempDir()
	src := "package x\n\nimport \"testing\"\n\nfunc TestHas(t *testing.T) { _ = \"a:read\" }\n\nfunc (r *T) TestMethod() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "a_test.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	if body, ok := functionBody(t, dir, "TestHas"); !ok || !strings.Contains(body, "a:read") {
		t.Errorf("TestHas: got %q, %v", body, ok)
	}
	if _, ok := functionBody(t, dir, "TestMissing"); ok {
		t.Error("found a test that is not there")
	}
	if _, ok := functionBody(t, dir, "TestMethod"); ok {
		t.Error("a method is not a test function")
	}
}
