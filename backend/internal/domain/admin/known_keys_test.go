package admin_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// KnownKeys is the set PATCH /admin/settings accepts (#421). A list kept by
// hand fails by omission: someone adds a Key constant, forgets this list, and
// the new setting is refused as unknown. So this reads the package's own
// constants and requires the two to agree in both directions.
func TestKnownKeys_IsEveryKeyConstant(t *testing.T) {
	fset := token.NewFileSet()
	declared := map[string]string{} // value -> constant name
	pkgs, err := parser.ParseDir(fset, ".", nil, 0)
	require.NoError(t, err)
	for _, pkg := range pkgs {
		for fname, file := range pkg.Files {
			if strings.HasSuffix(fname, "_test.go") {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				vs, ok := n.(*ast.ValueSpec)
				if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 || !strings.HasPrefix(vs.Names[0].Name, "Key") {
					return true
				}
				lit, ok := vs.Values[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				v, err := strconv.Unquote(lit.Value)
				require.NoError(t, err)
				declared[v] = vs.Names[0].Name
				return true
			})
		}
	}
	require.NotEmpty(t, declared)

	known := map[string]bool{}
	for _, k := range admin.KnownKeys() {
		require.False(t, known[k], "%q is listed twice", k)
		known[k] = true
	}
	for v, name := range declared {
		require.True(t, known[v], "%s (%q) is a setting key but KnownKeys does not list it, so PATCH /admin/settings would refuse it", name, v)
	}
	for k := range known {
		_, ok := declared[k]
		require.True(t, ok, "KnownKeys lists %q, which no Key constant declares", k)
	}
}

func TestIsKnownKey(t *testing.T) {
	require.True(t, admin.IsKnownKey(admin.KeySiteName))
	require.True(t, admin.IsKnownKey(admin.KeyAttachmentReputationVirusTotalKey))
	require.False(t, admin.IsKnownKey("site_nam"))
	require.False(t, admin.IsKnownKey(""))
	require.False(t, admin.IsKnownKey("oidc_client_secret_set"), "the dump's presence flags are not settings")
}

// The update check contacts a third party, so only a signed-in session may
// switch it on (the same rule as the reputation toggles). The record of the
// last check is kept by the server and is not a setting anybody may write.
func TestUpdateCheckKeys(t *testing.T) {
	require.True(t, admin.IsKnownKey(admin.KeyUpdateCheckEnabled))
	require.True(t, admin.IsKnownKey(admin.KeyPostUpgrade130Seen))
	require.True(t, admin.IsKnownKey(admin.KeyUpdateCheckResult))
	require.Contains(t, admin.AuthCriticalKeys(), admin.KeyUpdateCheckEnabled)
	require.True(t, admin.IsServerKeptKey(admin.KeyUpdateCheckResult))
	require.True(t, admin.IsServerKeptKey(admin.KeyPasswordRehashStartedAt))
	require.False(t, admin.IsServerKeptKey(admin.KeyUpdateCheckEnabled))
}
