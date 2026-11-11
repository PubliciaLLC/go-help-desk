package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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

// Where production code is looked for, relative to this package's directory:
// everything under backend/internal and backend/cmd, so a check in a package
// nobody listed is still seen. internal/domain/auth is the exception, because
// it is where the scope is defined; what it declares is followed by name
// instead (see authAliases).
var (
	parameterScopeRoots   = []string{"../../internal", "../../cmd"}
	parameterScopeAuthDir = "../domain/auth"
)

// isScopeLiteral reports whether n is a string literal that is exactly the
// scope's resource or the scope. Exactly: an error message that happens to
// contain the scope's name is text, not a use of it.
func isScopeLiteral(n ast.Node, p parameterScope) bool {
	lit, ok := n.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		return false
	}
	resource, _, _ := strings.Cut(p.scope, ":")
	return v == resource || v == p.scope
}

// mentions reports whether n contains the scope's resource: a name already
// known to stand for it, or a string literal that is the resource or the scope.
func mentions(n ast.Node, p parameterScope, names map[string]bool) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			found = found || names[id.Name]
		}
		found = found || isScopeLiteral(n, p)
		return !found
	})
	return found
}

// goFiles lists the non-test .go files under roots, skipping testdata and
// authDir.
func goFiles(t *testing.T, roots []string, authDir string) []string {
	t.Helper()
	skip, err := filepath.Abs(authDir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				abs, _ := filepath.Abs(path)
				if d.Name() == "testdata" || abs == skip {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				out = append(out, path)
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	return out
}

// authAliases returns every name declared in authDir that stands for the
// scope: the constant itself, and anything whose value mentions it, directly or
// through another such name. That covers
//   - consts and vars, exported or not, so a chain through an unexported
//     intermediate is followed;
//   - functions and methods, of any length, so auth.RequesterSearchScope(), a
//     helper that builds the scope in a few statements, and a method on a
//     credentials type all count.
//
// What separates a helper from the catalogue is not its length but what else it
// names: a declaration is followed only if it names no other Resource*
// constant. ScopesForRole and WithinRole name ResourceTickets and the rest,
// and `resources` and `writable` do too, so they are not followed, and neither
// is anything that reaches the scope only through them (All, ParseScope). Every
// caller of those would otherwise look like a handler check. The failure mode
// of this rule is a false "production uses it", which fails loudly naming the
// test that is required; it never hides a missing check.
//
// Names declared in other packages cannot be followed this way, and unexported
// names cannot be reached from outside auth, so they only matter as links.
func authAliases(t *testing.T, authDir string, p parameterScope) map[string]bool {
	t.Helper()
	names := map[string]bool{p.resourceConst: true}
	files, err := filepath.Glob(filepath.Join(authDir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	// What each declaration is called and what it is made of.
	type decl struct {
		names []string
		parts []ast.Node
	}
	var decls []decl
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), f, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, d := range file.Decls {
			switch d := d.(type) {
			case *ast.GenDecl:
				if d.Tok != token.CONST && d.Tok != token.VAR {
					continue
				}
				for _, s := range d.Specs {
					vs := s.(*ast.ValueSpec)
					dc := decl{}
					for _, n := range vs.Names {
						dc.names = append(dc.names, n.Name)
					}
					for _, v := range vs.Values {
						dc.parts = append(dc.parts, v)
					}
					decls = append(decls, dc)
				}
			case *ast.FuncDecl:
				// A method's name is what a selector says (auth.X.Method()),
				// so it is followed the same way.
				if d.Body != nil {
					decls = append(decls, decl{names: []string{d.Name.Name}, parts: []ast.Node{d.Body}})
				}
			}
		}
	}
	// The other resources' constants: naming one marks a declaration as the
	// catalogue or the role table, not as a way to ask for this scope.
	others := map[string]bool{}
	for _, dc := range decls {
		for _, n := range dc.names {
			if strings.HasPrefix(n, "Resource") && n != p.resourceConst {
				others[n] = true
			}
		}
	}
	namesOther := func(n ast.Node) bool {
		found := false
		ast.Inspect(n, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				found = found || others[id.Name]
			}
			return !found
		})
		return found
	}
	// Repeat until nothing new is found, so a chain of any length is followed.
	for changed := true; changed; {
		changed = false
		for _, dc := range decls {
			for _, part := range dc.parts {
				if !mentions(part, p, names) || namesOther(part) {
					continue
				}
				for _, n := range dc.names {
					if n != "_" && !names[n] {
						names[n] = true
						changed = true
					}
				}
			}
		}
	}
	return names
}

// referencedByProduction reports whether non-test code outside authDir uses the
// scope: a string literal that is the resource or the scope, or a selector
// (auth.X) naming the constant or one of its aliases. Comments are not parsed
// in, so a note about the scope is not mistaken for the check.
func referencedByProduction(t *testing.T, roots []string, authDir string, p parameterScope) bool {
	t.Helper()
	aliases := authAliases(t, authDir, p)
	for _, f := range goFiles(t, roots, authDir) {
		file, err := parser.ParseFile(token.NewFileSet(), f, nil, 0) // mode 0 drops comments
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		found := false
		ast.Inspect(file, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				found = found || aliases[sel.Sel.Name]
			}
			found = found || isScopeLiteral(n, p)
			return !found
		})
		if found {
			return true
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
func checkParameterScope(t *testing.T, roots []string, authDir, testDir string, p parameterScope) (guardState, string) {
	t.Helper()
	if !referencedByProduction(t, roots, authDir, p) {
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
		state, msg := checkParameterScope(t, parameterScopeRoots, parameterScopeAuthDir, ".", p)
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
	write := func(t *testing.T, root, name, src string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const (
		authDecl         = "package auth\n\nconst ResourceXSearch = \"x_search\"\n"
		authAlias        = authDecl + "\nconst XSearchScope = ResourceXSearch + \":read\"\n"
		authAliasOfAlias = authAlias + "\nvar XSearchAgain = XSearchScope\n"
		authLiteralAlias = authDecl + "\nvar XSearchKey = \"x_search:read\"\n"
		authUnexported   = authDecl + "\nvar xSearchScope = ResourceXSearch\n"
		handlerViaConst  = "package y\n\nfunc h() { _ = auth.ResourceXSearch }\n"
		// An unexported link in the chain, and a getter function.
		authViaPrivate  = authDecl + "\nvar xSearchScope = ResourceXSearch\n\nvar XSearchViaPrivate = xSearchScope\n"
		authGetter      = authDecl + "\nfunc XSearchScope() string { return ResourceXSearch + \":read\" }\n"
		authGetterViaFn = authDecl + "\nfunc xSearch() string { return ResourceXSearch }\n\nfunc XSearchScope() string { return xSearch() + \":read\" }\n"
		// The role table names the other resources too, which is what tells it
		// from a helper for this scope (see authAliases).
		authOther     = authDecl + "\nconst ResourceOther = \"other\"\n"
		authCatalogue = authOther + "\nfunc ScopesFor(role string) []string {\n\tswitch role {\n\tcase \"staff\":\n\t\treturn []string{ResourceXSearch, ResourceOther}\n\t}\n\treturn nil\n}\n"
		// A one-statement function is not a helper if it names another resource.
		authOneReturnCatalogue = authOther + "\nfunc Everything() []string { return []string{ResourceXSearch, ResourceOther} }\n"
		// The list of every resource, and a function that only reaches the
		// scope through it.
		authViaList = authOther + "\nvar resources = []string{ResourceXSearch, ResourceOther}\n\nfunc All() []string {\n\tout := []string{}\n\tout = append(out, resources...)\n\treturn out\n}\n"
		// Helpers of the shapes Fable's review of #640 found missed: a
		// multi-statement function, and a method.
		authMultiStatement   = authDecl + "\ntype Scope struct{ Resource, Action string }\n\nfunc XSearchScope() Scope {\n\ts := Scope{ResourceXSearch, \"read\"}\n\treturn s\n}\n"
		authMethod           = authDecl + "\ntype Granted []string\n\nfunc (g Granted) XSearch() bool {\n\tneed := ResourceXSearch + \":read\"\n\tfor _, s := range g {\n\t\tif s == need {\n\t\t\treturn true\n\t\t}\n\t}\n\treturn false\n}\n"
		handlerViaMethod     = "package y\n\nfunc h() { _ = auth.Granted(nil).XSearch() }\n"
		handlerViaEverything = "package y\n\nfunc h() { _ = auth.Everything() }\n"
		handlerViaAll        = "package y\n\nfunc h() { _ = auth.All() }\n"
		handlerViaPriv       = "package y\n\nfunc h() { _ = auth.XSearchViaPrivate }\n"
		handlerViaGetter     = "package y\n\nfunc h() { _ = auth.XSearchScope() }\n"
		handlerViaCatlg      = "package y\n\nfunc h() { _ = auth.ScopesFor(\"staff\") }\n"
		handlerMessage       = "package y\n\nvar errMsg = \"this credential does not carry the x_search:read scope\"\n"
		handlerViaAlias      = "package y\n\nfunc h() { _ = auth.XSearchScope }\n"
		handlerViaAlias2     = "package y\n\nfunc h() { _ = auth.XSearchAgain }\n"
		handlerViaKey        = "package y\n\nfunc h() { _ = auth.XSearchKey }\n"
		handlerViaLiteral    = "package y\n\nfunc h() { _ = \"x_search:read\" }\n"
		handlerUnrelated     = "package y\n\nfunc h() { _ = auth.ResourceTickets }\n"
		commentOnly          = "package y\n\n// x_search:read and ResourceXSearch are mentioned here only.\nfunc h() {}\n"
		goodTest             = "package server\n\nimport \"testing\"\n\nfunc TestHandlerNeedsScope(t *testing.T) { _ = \"x_search:read\" }\n"
		emptyTest            = "package server\n\nimport \"testing\"\n\nfunc TestHandlerNeedsScope(t *testing.T) {}\n"
		renamedTest          = "package server\n\nimport \"testing\"\n\nfunc TestSomethingElse(t *testing.T) { _ = \"x_search:read\" }\n"
	)
	// The tree mirrors the real one: auth, the server tests, and any other
	// package ("newpkg"), none of them on a list the guard was told about.
	type files = map[string]string

	cases := []struct {
		name  string
		files files
		want  guardState
	}{
		{"nothing uses the scope yet", files{"auth/a.go": authDecl, "newpkg/a.go": commentOnly}, guardPending},
		{"only auth mentions it", files{"auth/a.go": authAlias}, guardPending},
		{"only a test mentions it", files{"auth/a.go": authDecl, "server/a_test.go": goodTest}, guardPending},
		{"a test file in another package uses it", files{"auth/a.go": authDecl, "newpkg/a_test.go": handlerViaConst}, guardPending},
		{"testdata is not production", files{"auth/a.go": authDecl, "newpkg/testdata/a.go": handlerViaConst}, guardPending},
		{"an unrelated auth name", files{"auth/a.go": authAlias, "newpkg/a.go": handlerUnrelated}, guardPending},
		{"an alias nobody uses, and an unexported one", files{"auth/a.go": authUnexported, "newpkg/a.go": handlerViaAlias}, guardPending},

		// Error text that names the scope is not a use of it (#639's filter.go).
		{"the scope named inside a longer message", files{"auth/a.go": authDecl, "newpkg/a.go": handlerMessage}, guardPending},
		// A function with a body, like the role table, mentions the scope
		// without being a way to ask for it.
		{"a role table function is not an alias", files{"auth/a.go": authCatalogue, "newpkg/a.go": handlerViaCatlg}, guardPending},

		{"production uses the constant, no test", files{"auth/a.go": authDecl, "server/a.go": handlerViaConst}, guardFails},
		{"production uses the literal, no test", files{"auth/a.go": authDecl, "server/a.go": handlerViaLiteral}, guardFails},
		// Fable's two holes.
		{"the check is in a package nobody listed", files{"auth/a.go": authDecl, "newpkg/deep/a.go": handlerViaConst}, guardFails},
		{"the check goes through an alias declared in auth", files{"auth/a.go": authAlias, "newpkg/a.go": handlerViaAlias}, guardFails},
		// Fable's two remaining holes.
		{"a chain through an unexported intermediate", files{"auth/a.go": authViaPrivate, "newpkg/a.go": handlerViaPriv}, guardFails},
		{"an exported function that returns the scope", files{"auth/a.go": authGetter, "newpkg/a.go": handlerViaGetter}, guardFails},
		{"a getter through an unexported function", files{"auth/a.go": authGetterViaFn, "newpkg/a.go": handlerViaGetter}, guardFails},
		// Fable's review of #640: what separates a helper from the catalogue is
		// what else it names, not how many statements it has.
		{"a helper of several statements", files{"auth/a.go": authMultiStatement, "newpkg/a.go": handlerViaGetter}, guardFails},
		{"a method", files{"auth/a.go": authMethod, "newpkg/a.go": handlerViaMethod}, guardFails},
		{"a one-statement function that names another resource is the catalogue", files{"auth/a.go": authOneReturnCatalogue, "newpkg/a.go": handlerViaEverything}, guardPending},
		{"a function that reaches the scope only through the list of every resource", files{"auth/a.go": authViaList, "newpkg/a.go": handlerViaAll}, guardPending},
		{"an alias of an alias", files{"auth/a.go": authAliasOfAlias, "newpkg/a.go": handlerViaAlias2}, guardFails},
		{"an alias declared as a literal", files{"auth/a.go": authLiteralAlias, "newpkg/a.go": handlerViaKey}, guardFails},

		{"the test is named differently", files{"auth/a.go": authDecl, "server/a.go": handlerViaConst, "server/a_test.go": renamedTest}, guardFails},
		{"the test never names the scope", files{"auth/a.go": authDecl, "server/a.go": handlerViaConst, "server/a_test.go": emptyTest}, guardFails},
		{"the check is elsewhere and the test is named differently", files{"auth/a.go": authAlias, "newpkg/a.go": handlerViaAlias, "server/a_test.go": renamedTest}, guardFails},
		{"production uses it and the test names it", files{"auth/a.go": authDecl, "newpkg/a.go": handlerViaConst, "server/a_test.go": goodTest}, guardOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for name, src := range tc.files {
				write(t, root, name, src)
			}
			got, msg := checkParameterScope(t, []string{root}, filepath.Join(root, "auth"), filepath.Join(root, "server"), p)
			if got != tc.want {
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
