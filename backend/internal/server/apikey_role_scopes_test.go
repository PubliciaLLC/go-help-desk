package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// scopesTheRouterGives measures, from the router and nothing else, which scopes
// an API key owned by role can use: for every route its own middleware admits
// that role's key to, the smallest scope set that gets through. It reuses the
// middleware chain the OpenAPI auth probe runs (probeIgnored, minimalScopes),
// so it sees exactly what a request would.
//
// A route that admits a key holding no scope at all (GET /me) needs nothing and
// contributes nothing; a route that refuses machine credentials outright
// (/me/api-keys, /me/password) contributes nothing either.
func scopesTheRouterGives(t *testing.T, role user.Role) []string {
	t.Helper()
	keys := map[string]probeKey{}
	routes := walkRouter(t, routeOnlyServer(keys))

	var all []string
	for _, sc := range auth.All() {
		all = append(all, sc.String())
	}

	used := map[string]bool{}
	n := 0
	for _, r := range routes {
		var chain []func(http.Handler) http.Handler
		guest := false
		for _, m := range r.mws {
			name := funcName(m)
			switch {
			case slices.ContainsFunc(probeIgnored, func(s string) bool { return strings.Contains(name, s) }):
			case strings.Contains(name, ".requireTicketAccess"), strings.Contains(name, ".requireSignedInOrGuestsEnabled"):
			case strings.Contains(name, "internal/middleware.GuestAuth"):
				guest = true
			case strings.Contains(name, "internal/middleware."):
				chain = append(chain, m)
			default:
				t.Fatalf("%s %s: the probe does not know middleware %s", r.method, r.path, name)
			}
		}
		if guest {
			continue
		}
		admits := func(header, value string) bool {
			reached := false
			var h http.Handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })
			for i := len(chain) - 1; i >= 0; i-- {
				h = chain[i](h)
			}
			req := httptest.NewRequest(r.method, "/", nil)
			req.Header.Set(header, value)
			h.ServeHTTP(httptest.NewRecorder(), req)
			return reached
		}
		keyFor := func(scopes []string) bool {
			n++
			raw := fmt.Sprintf("role-scope-key-%d", n)
			keys[auth.HashToken(raw)] = probeKey{role, scopes}
			return admits("Authorization", apiKeyPrefix+raw)
		}
		if !admits("X-Probe-Session", string(role)+":mfa") || !keyFor(all) {
			continue
		}
		for _, sc := range minimalScopes(all, keyFor) {
			used[sc] = true
		}
	}
	out := make([]string, 0, len(used))
	for sc := range used {
		out = append(out, sc)
	}
	sort.Strings(out)
	return out
}

func offeredTo(role user.Role) []string {
	var out []string
	for _, sc := range auth.ScopesForRole(string(role)) {
		out = append(out, sc.String())
	}
	sort.Strings(out)
	return out
}

// A key's scopes can never exceed its owner's role (#450). auth.ScopesForRole
// is the table that decides which scopes a key may carry; this derives the same
// answer from the router and fails when they differ in either direction.
//
// The case it exists for: a scope only admin routes use being offered to staff
// or a requester. Scopes narrow rather than grant, so such a key would still be
// refused every admin route, but a credential that looks like it can edit users
// and cannot is a trap, and the next route someone loosens to staff would turn
// it live.
//
// The other direction matters too. A scope the router uses for staff but the
// table omits is a key that cannot be given the access its owner has.
func TestScopesForRole_MatchTheRouter(t *testing.T) {
	for _, role := range []user.Role{user.RoleAdmin, user.RoleStaff, user.RoleUser} {
		t.Run(string(role), func(t *testing.T) {
			derived := scopesTheRouterGives(t, role)
			offered := offeredTo(role)

			for _, sc := range offered {
				if !slices.Contains(derived, sc) {
					t.Errorf("%s is offered to %s keys, but no route a %s key can reach needs it", sc, role, role)
				}
			}
			for _, sc := range derived {
				if !slices.Contains(offered, sc) {
					t.Errorf("a %s key needs %s to reach a route, but auth.ScopesForRole does not offer it", role, sc)
				}
			}
		})
	}
}
