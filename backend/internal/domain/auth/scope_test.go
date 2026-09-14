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
			granted:  []string{"tickets", "tickets:*", "*", "", "::"},
			required: read, want: false,
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
