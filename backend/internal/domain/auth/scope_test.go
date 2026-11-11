package auth_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

func TestAllows(t *testing.T) {
	read := auth.Scope{Resource: auth.ResourceTickets, Action: auth.ActionRead}
	write := auth.Scope{Resource: auth.ResourceTickets, Action: auth.ActionWrite}

	cases := []struct {
		name     string
		granted  []string
		required auth.Scope
		want     bool
	}{
		{
			// The whole point of the change: before enforcement, credentials
			// carried no scopes and could do everything.
			name: "no scopes denies", granted: nil, required: read, want: false,
		},
		{name: "empty slice denies", granted: []string{}, required: read, want: false},
		{name: "exact read", granted: []string{"tickets:read"}, required: read, want: true},
		{name: "exact write", granted: []string{"tickets:write"}, required: write, want: true},
		{
			name: "write implies read", granted: []string{"tickets:write"},
			required: read, want: true,
		},
		{
			name: "read does NOT imply write", granted: []string{"tickets:read"},
			required: write, want: false,
		},
		{
			name:    "another resource does not carry over",
			granted: []string{"users:write"}, required: write, want: false,
		},
		{
			name:     "one match among several is enough",
			granted:  []string{"users:read", "tickets:read", "tags:write"},
			required: read, want: true,
		},
		{
			// A row edited by hand must not become a wildcard.
			name:     "malformed entries are ignored, not trusted",
			granted:  []string{"tickets", "tickets:*", "*", "", "::", "tickets:READ"},
			required: read, want: false,
		},
		{
			// #371: a key stored with audit:write before it was refused does
			// not reach the audit view through write-implies-read.
			name:     "audit:write grants nothing, not even audit:read",
			granted:  []string{"audit:write"},
			required: auth.Scope{Resource: auth.ResourceAudit, Action: auth.ActionRead}, want: false,
		},
		{
			name:     "audit:read reaches the audit view",
			granted:  []string{"audit:read"},
			required: auth.Scope{Resource: auth.ResourceAudit, Action: auth.ActionRead}, want: true,
		},
		{
			name:    "a malformed entry does not poison a valid one",
			granted: []string{"nonsense", "tickets:read"}, required: read, want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, auth.Allows(tc.granted, tc.required))
		})
	}
}

func TestParseScope(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{name: "valid read", in: "tickets:read"},
		{name: "valid write", in: "credentials:write"},
		{name: "underscored resource", in: "canned_responses:read"},
		{name: "audit read", in: "audit:read"},
		// #371: only the running server writes the audit log.
		{name: "audit has no write scope", in: "audit:write", wantErr: true},
		{name: "no colon", in: "tickets", wantErr: true},
		{name: "unknown action", in: "tickets:delete", wantErr: true},
		{name: "unknown resource", in: "sprockets:read", wantErr: true},
		{name: "wildcard is not a scope", in: "tickets:*", wantErr: true},
		{name: "empty", in: "", wantErr: true},
		// Case matters: accepting Tickets:Read would mean two spellings of one
		// scope, and only one of them would match at enforcement time.
		{name: "wrong case rejected", in: "Tickets:Read", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := auth.ParseScope(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

// Every scope All() advertises must be one ParseScope accepts. The admin UI
// builds its picker from All(), so a mismatch would offer a scope that is
// rejected on save.
func TestAll_IsSelfConsistent(t *testing.T) {
	all := auth.All()
	require.NotEmpty(t, all)

	seen := map[string]bool{}
	for _, s := range all {
		str := s.String()
		require.False(t, seen[str], "duplicate scope %q", str)
		seen[str] = true

		parsed, err := auth.ParseScope(str)
		require.NoError(t, err, "All() advertises %q which ParseScope rejects", str)
		require.Equal(t, s, parsed)
	}
	require.NoError(t, auth.ValidateScopes(scopeStrings(all)))
}

func scopeStrings(in []auth.Scope) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = s.String()
	}
	return out
}

func TestValidateScopes(t *testing.T) {
	require.NoError(t, auth.ValidateScopes(nil))
	require.NoError(t, auth.ValidateScopes([]string{"tickets:read", "users:write"}))

	err := auth.ValidateScopes([]string{"tickets:read", "bogus:read"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "bogus", "the error must name the offending scope")
}

// There is no wildcard scope, and nothing that looks like one may act as one.
//
// A credential that should reach everything lists every scope it needs. That
// keeps what it can do legible from the credential itself, and it means adding
// a resource later does not silently widen every credential that already
// exists — which is exactly how "scopes" came to mean nothing the first time.
func TestNoWildcardScopeExists(t *testing.T) {
	notScopes := []string{
		"*", "*:*", "**", "tickets:*", "*:read", "all", "admin", "any",
		" *", "* ", "*,tickets:read", ".*", "%",
	}

	for _, ns := range notScopes {
		t.Run("rejected at creation: "+ns, func(t *testing.T) {
			_, err := auth.ParseScope(ns)
			require.Error(t, err, "%q must not parse as a scope", ns)
			require.Error(t, auth.ValidateScopes([]string{ns}),
				"%q must be refused when a credential is created", ns)
		})

		t.Run("grants nothing: "+ns, func(t *testing.T) {
			for _, s := range auth.All() {
				require.False(t, auth.Allows([]string{ns}, s),
					"%q must not grant %s", ns, s)
			}
		})
	}
}

// The only way to hold every scope is to hold every scope. This pins the cost
// of the decision as well as the decision: the list is long on purpose.
func TestFullAccessRequiresEveryScopeListed(t *testing.T) {
	all := scopeStrings(auth.All())
	for _, s := range auth.All() {
		require.True(t, auth.Allows(all, s), "the full list must grant %s", s)
	}

	// Drop one, and exactly that one stops working.
	withoutTicketsWrite := make([]string, 0, len(all))
	for _, s := range all {
		if s != "tickets:write" {
			withoutTicketsWrite = append(withoutTicketsWrite, s)
		}
	}
	require.False(t, auth.Allows(withoutTicketsWrite,
		auth.Scope{Resource: auth.ResourceTickets, Action: auth.ActionWrite}),
		"removing one scope must remove exactly that access")
	require.True(t, auth.Allows(withoutTicketsWrite,
		auth.Scope{Resource: auth.ResourceGroups, Action: auth.ActionWrite}),
		"and must leave the rest alone")
}

// #371: the audit log is written only by the running server, so no machine
// credential may hold audit:write. It is refused at creation, offered by no
// picker, cannot be passed on by a credential that holds it, and grants
// nothing if a row already carries it.
func TestAuditHasNoWriteScope(t *testing.T) {
	all := scopeStrings(auth.All())
	require.Contains(t, all, "audit:read")
	require.NotContains(t, all, "audit:write", "the picker must not offer audit:write")

	err := auth.ValidateScopes([]string{"tickets:read", "audit:write"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "audit:write", "the error must name the offending scope")

	_, ok := auth.Subset([]string{"audit:write"}, []string{"audit:write"})
	require.False(t, ok, "a credential holding audit:write must not pass it on")
}

// ScopesForRole is the most a key owned by that role may carry (#450). The
// router-derived check lives in internal/server; this pins the shape.
func TestScopesForRole(t *testing.T) {
	has := func(scopes []auth.Scope, want string) bool {
		for _, s := range scopes {
			if s.String() == want {
				return true
			}
		}
		return false
	}

	t.Run("an administrator may carry every scope", func(t *testing.T) {
		require.Equal(t, auth.All(), auth.ScopesForRole("admin"))
	})

	for _, role := range []string{"staff"} {
		t.Run(role+" gets no administrative scope", func(t *testing.T) {
			got := auth.ScopesForRole(role)
			require.NotEmpty(t, got)
			require.NoError(t, auth.ValidateScopes(scopeStrings(got)), "an offered scope must be one ParseScope accepts")
			for _, adminOnly := range []string{
				"users:write", "settings:read", "settings:write",
				"credentials:read", "credentials:write", "webhooks:write", "plugins:write",
				"categories:write", "sla:write", "tags:write", "canned_responses:write",
				// The admin-wide audit view is administrators only.
				"audit:read",
			} {
				require.False(t, has(got, adminOnly), "%s must not be offered to %s", adminOnly, role)
			}
		})
	}

	// Requesters never own a key (#450), so there is nothing to offer them.
	t.Run("a reporting user is offered nothing", func(t *testing.T) {
		require.Empty(t, auth.ScopesForRole("user"))
		require.Error(t, auth.WithinRole("user", []string{"tickets:read"}))
	})

	t.Run("a role it does not know gets nothing", func(t *testing.T) {
		require.Empty(t, auth.ScopesForRole(""))
		require.Empty(t, auth.ScopesForRole("superuser"))
	})
}

// users:write opens nothing to a key since keys can't write users (2026-10-16),
// so the pickers and the catalogue no longer offer it. ParseScope still accepts
// it, so a stored key or a script that names it keeps working.
func TestUsersWriteIsNotOfferedButStillParses(t *testing.T) {
	require.NotContains(t, scopeStrings(auth.All()), "users:write")
	require.Contains(t, scopeStrings(auth.All()), "users:read")
	for _, role := range []string{"admin", "staff"} {
		require.NotContains(t, scopeStrings(auth.ScopesForRole(role)), "users:write", role)
	}
	_, err := auth.ParseScope("users:write")
	require.NoError(t, err, "stored keys and scripts that name it must keep working")
	require.NoError(t, auth.ValidateScopes([]string{"users:write"}))
	require.True(t, auth.Allows([]string{"users:write"}, auth.Scope{Resource: auth.ResourceUsers, Action: auth.ActionRead}),
		"and write still implies read")
}

// requester_search:read gates a query parameter (free-text requester search on
// GET /tickets), not a route. Free text over many visible tickets maps names and
// addresses to tickets in bulk, which #290 took away from machine credentials,
// so only an explicit grant gives it back.
func TestRequesterSearchScope(t *testing.T) {
	const scope = "requester_search:read"
	need := auth.Scope{Resource: auth.ResourceRequesterSearch, Action: auth.ActionRead}

	t.Run("it is in the catalogue, read only", func(t *testing.T) {
		require.Contains(t, scopeStrings(auth.All()), scope)
		require.NotContains(t, scopeStrings(auth.All()), "requester_search:write")
		require.NoError(t, auth.ValidateScopes([]string{scope}))
	})

	// Same refusal as audit:write, same reason: there is nothing to write.
	t.Run("there is no write scope", func(t *testing.T) {
		err := auth.ValidateScopes([]string{"requester_search:write"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "requester_search:write")
		_, ok := auth.Subset([]string{"requester_search:write"}, []string{"requester_search:write"})
		require.False(t, ok)
	})

	// Rule 4: no wildcard, and a key that exists today must not gain free-text
	// search when this ships. Nothing else implies it, write scopes included.
	t.Run("nothing implies it", func(t *testing.T) {
		for _, granted := range [][]string{
			{"tickets:read"}, {"tickets:write"}, {"users:read"}, {"users:write"},
			{"audit:read"}, {"credentials:write"}, {"settings:write"},
			{"*"}, {"requester_search:*"}, {"requester_search:write"}, {"requester:read"},
		} {
			require.False(t, auth.Allows(granted, need), "%v must not grant %s", granted, scope)
		}
		require.True(t, auth.Allows([]string{scope}, need))
	})

	t.Run("it implies nothing else", func(t *testing.T) {
		for _, other := range []string{"tickets:read", "users:read", "audit:read"} {
			sc, err := auth.ParseScope(other)
			require.NoError(t, err)
			require.False(t, auth.Allows([]string{scope}, sc), "%s must not grant %s", scope, other)
		}
	})

	t.Run("staff and administrators may hold it, requesters may not", func(t *testing.T) {
		require.Contains(t, scopeStrings(auth.ScopesForRole("admin")), scope)
		require.Contains(t, scopeStrings(auth.ScopesForRole("staff")), scope)
		require.NoError(t, auth.WithinRole("admin", []string{scope}))
		require.NoError(t, auth.WithinRole("staff", []string{scope}))
		require.Error(t, auth.WithinRole("user", []string{scope}))
		require.Error(t, auth.WithinRole("", []string{scope}))
	})

	// A credential cannot pass on a search permission it does not hold.
	t.Run("a credential cannot mint it for itself", func(t *testing.T) {
		_, ok := auth.Subset([]string{"credentials:write", "tickets:write"}, []string{scope})
		require.False(t, ok)
	})
}
