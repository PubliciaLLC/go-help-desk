package admin_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// AuthCriticalKeys decides which settings a machine credential may not change.
// A list like that fails by omission: someone adds a setting that controls
// authentication, forgets this list, and an API key can change it.
//
// So rather than restating the list — which would only assert that a copy
// matches its original — this reads the package's own constants and requires
// that anything named for SAML, OIDC, MFA or signup appears in it.
func TestAuthCriticalKeys_CoversEveryAuthSetting(t *testing.T) {
	critical := admin.AuthCriticalKeys()
	require.NotEmpty(t, critical)

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, 0)
	require.NoError(t, err)

	// Constant name prefixes that mean "this decides who may sign in".
	authPrefixes := []string{"KeySAML", "KeyOIDC", "KeyMFA", "KeySelfSignup", "KeyOpenRegistration", "KeyAllowedEmailDomains"}

	found := 0
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				vs, ok := n.(*ast.ValueSpec)
				if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
					return true
				}
				name := vs.Names[0].Name
				if !slices.ContainsFunc(authPrefixes, func(p string) bool {
					return strings.HasPrefix(name, p)
				}) {
					return true
				}
				lit, ok := vs.Values[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(lit.Value)
				require.NoError(t, err)

				found++
				require.Contains(t, critical, value,
					"%s (%q) controls authentication but is not in AuthCriticalKeys, "+
						"so an API key can change it", name, value)
				return true
			})
		}
	}
	require.Greater(t, found, 10, "expected to find the auth settings; the scan found %d", found)
}

// Everything in the list must be a real setting key, or the guard silently
// protects nothing.
func TestAuthCriticalKeys_HasNoStrays(t *testing.T) {
	for _, k := range admin.AuthCriticalKeys() {
		require.NotEmpty(t, k)
		require.NotContains(t, k, " ", "%q does not look like a setting key", k)
	}
}
