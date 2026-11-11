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
	// resourceConst is the auth constant production code uses for the scope's
	// resource. Production code that names it, or the resource as a string
	// literal, is the handler check (see checkParameterScope).
	resourceConst string
}

var parameterScopes = []parameterScope{
	{
		scope:         "requester_search:read",
		roles:         []user.Role{user.RoleAdmin, user.RoleStaff},
		route:         "GET /api/v1/tickets",
		param:         "requester",
		handlerTest:   "TestListTickets_RequesterSearchNeedsTheScope",
		resourceConst: "ResourceRequesterSearch",
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

// Where the handler check may live, relative to this package's directory: the
// REST handlers, the ticket domain, the MCP tool and middleware.
var parameterScopeProductionDirs = []string{".", "../domain/ticket", "../mcp", "../middleware"}

// referencedByProduction reports whether a non-test .go file in dirs uses the
// scope's resource, as the auth constant or as a string literal. Comments are
// not parsed in, so a note about the scope is not mistaken for the check.
func referencedByProduction(t *testing.T, dirs []string, p parameterScope) bool {
	t.Helper()
	resource, _, _ := strings.Cut(p.scope, ":")
	for _, dir := range dirs {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(token.NewFileSet(), f, nil, 0) // mode 0 drops comments
			if err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			found := false
			ast.Inspect(file, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.Ident:
					found = found || n.Name == p.resourceConst
				case *ast.BasicLit:
					found = found || (n.Kind == token.STRING && strings.Contains(n.Value, resource))
				}
				return !found
			})
			if found {
				return true
			}
		}
	}
	return false
}

type guardState int

const (
	guardPending guardState = iota // no production code uses the scope yet
	guardOK
	guardFails
)

// checkParameterScope decides what a parameter scope owes. While no production
// code uses the scope there is no check to test, so the entry is pending. The
// moment production code uses it, the named handler test must exist and name
// the scope. Pending is tied to production code, not to a flag someone must
// remember to clear: a handler test named differently from handlerTest cannot
// hide behind it.
func checkParameterScope(t *testing.T, prodDirs []string, testDir string, p parameterScope) (guardState, string) {
	t.Helper()
	if !referencedByProduction(t, prodDirs, p) {
		return guardPending, "no production code uses " + p.scope + " yet"
	}
	body, found := functionBody(t, testDir, p.handlerTest)
	switch {
	case !found:
		return guardFails, "production code uses " + p.scope + " but no test named " + p.handlerTest + " exists"
	case !strings.Contains(body, p.scope):
		return guardFails, p.handlerTest + " never names " + p.scope + ", so it cannot be checking it"
	}
	return guardOK, ""
}

// Every parameter scope must be exercised by a handler test: the router test
// cannot see it, so without this a handler could forget the check and nothing
// would fail.
func TestParameterScopes_AreCheckedByAHandlerTest(t *testing.T) {
	for _, p := range parameterScopes {
		state, msg := checkParameterScope(t, parameterScopeProductionDirs, ".", p)
		if state == guardPending {
			// On the parent, so the skip is reported once at the top and not
			// only inside a subtest.
			t.Logf("PENDING: %s: %s; %s is required from the change that adds the check", p.scope, msg, p.handlerTest)
		}
		t.Run(p.scope, func(t *testing.T) {
			switch state {
			case guardPending:
				t.Skip(msg)
			case guardFails:
				t.Fatal(msg)
			}
		})
	}
}

// The guard is only worth having if it can fail. This runs it against small
// directories in each state it can be in.
func TestCheckParameterScope_States(t *testing.T) {
	p := parameterScope{
		scope: "x_search:read", handlerTest: "TestHandlerNeedsScope", resourceConst: "ResourceXSearch",
	}
	write := func(t *testing.T, dir, name, src string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const (
		handlerViaConst   = "package x\n\nfunc h() { _ = auth.ResourceXSearch }\n"
		handlerViaLiteral = "package x\n\nfunc h() { _ = \"x_search:read\" }\n"
		commentOnly       = "package x\n\n// x_search:read and ResourceXSearch are mentioned here only.\nfunc h() {}\n"
		goodTest          = "package x\n\nimport \"testing\"\n\nfunc TestHandlerNeedsScope(t *testing.T) { _ = \"x_search:read\" }\n"
		emptyTest         = "package x\n\nimport \"testing\"\n\nfunc TestHandlerNeedsScope(t *testing.T) {}\n"
		renamedTest       = "package x\n\nimport \"testing\"\n\nfunc TestSomethingElse(t *testing.T) { _ = \"x_search:read\" }\n"
	)

	cases := []struct {
		name  string
		files map[string]string // in one directory, for production and tests alike
		want  guardState
	}{
		{"nothing uses the scope yet", map[string]string{"a.go": commentOnly}, guardPending},
		{"only a test mentions it", map[string]string{"a_test.go": goodTest}, guardPending},
		{"production uses the constant, no test", map[string]string{"a.go": handlerViaConst}, guardFails},
		{"production uses the literal, no test", map[string]string{"a.go": handlerViaLiteral}, guardFails},
		{"production uses it, the test is named differently", map[string]string{"a.go": handlerViaConst, "a_test.go": renamedTest}, guardFails},
		{"production uses it, the test never names the scope", map[string]string{"a.go": handlerViaConst, "a_test.go": emptyTest}, guardFails},
		{"production uses it and the test names it", map[string]string{"a.go": handlerViaConst, "a_test.go": goodTest}, guardOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, src := range tc.files {
				write(t, dir, name, src)
			}
			if got, msg := checkParameterScope(t, []string{dir}, dir, p); got != tc.want {
				t.Errorf("state = %d (%s), want %d", got, msg, tc.want)
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
