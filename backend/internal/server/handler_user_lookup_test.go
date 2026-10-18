package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// #444: the requester picker on the New ticket page. A lookup of accounts by
// name or address for staff and administrators, bounded and narrow: it must
// never be a way to list the directory, and a requester gets nothing from it.

type lookupHit struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}

func lookupPath(q string) string { return "/api/v1/users/lookup?q=" + url.QueryEscape(q) }

func decodeLookup(t *testing.T, res *http.Response) []lookupHit {
	t.Helper()
	body := readBody(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	hits := []lookupHit{}
	require.NoError(t, json.Unmarshal([]byte(body), &hits))
	return hits
}

func TestUserLookup_StaffAndAdminFindAccounts(t *testing.T) {
	for _, caller := range []string{"staff", "admin"} {
		t.Run(caller, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			do := h.do
			if caller == "admin" {
				do = h.doAsAdmin
			}

			// By name, and by address, in any case.
			for _, q := range []string{"Reporting", "REPORTING USER", "user@test", "USER@TEST.LOCAL"} {
				hits := decodeLookup(t, do(t, http.MethodGet, lookupPath(q), nil))
				var found bool
				for _, hit := range hits {
					if hit.ID == h.userID.String() {
						found = true
						require.Equal(t, "Reporting User", hit.DisplayName)
						require.Equal(t, "user@test.local", hit.Email)
					}
				}
				require.True(t, found, "q=%q did not find the account", q)
			}
		})
	}
}

// Only what the picker draws: an id, a name and an address. Role, login state
// and the rest stay the administrator's view.
func TestUserLookup_ReturnsOnlyWhatThePickerNeeds(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.do(t, http.MethodGet, lookupPath("Reporting"), nil)
	body := readBody(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &rows))
	require.NotEmpty(t, rows)
	for _, row := range rows {
		keys := []string{}
		for k := range row {
			keys = append(keys, k)
		}
		require.ElementsMatch(t, []string{"id", "display_name", "email"}, keys)
	}
}

func TestUserLookup_RequesterAndAnonymousAreRefused(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.doAsUser(t, http.MethodGet, lookupPath("Reporting"), nil)
	body := readBody(t, res)
	require.Equal(t, http.StatusForbidden, res.StatusCode, body)
	require.NotContains(t, body, "user@test.local", "the refusal leaks nothing")

	res = h.doUnauth(t, http.MethodGet, lookupPath("Reporting"), nil)
	res.Body.Close()
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
}

// A credential needs users:read, the scope that already guards the user list;
// tickets:write alone (enough to file a ticket by id) does not open a directory
// of addresses.
func TestUserLookup_NeedsTheUsersReadScope(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	for scopes, want := range map[string]int{
		"tickets:write": http.StatusForbidden,
		"tickets:read":  http.StatusForbidden,
		"users:read":    http.StatusOK,
	} {
		res := withKey(t, h, mintKey(t, h, []string{scopes}), http.MethodGet, lookupPath("Reporting"), nil)
		res.Body.Close()
		require.Equal(t, want, res.StatusCode, scopes)
	}
}

// Search-as-you-type with a floor and a ceiling: no query, or a one-character
// one, is a refusal rather than the directory, and a broad query gets a capped
// answer.
func TestUserLookup_IsBounded(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	for i := range 15 {
		_, err := h.userSvc.Create(ctx, user.CreateUserInput{
			Email: fmt.Sprintf("bulk%02d@test.local", i), DisplayName: fmt.Sprintf("Bulk Person %02d", i),
			Role: user.RoleUser, Password: "a-real-passphrase",
		})
		require.NoError(t, err)
	}

	for _, path := range []string{
		"/api/v1/users/lookup",
		"/api/v1/users/lookup?q=",
		"/api/v1/users/lookup?q=b",
		lookupPath("   "),
		lookupPath(" b "),
	} {
		res := h.do(t, http.MethodGet, path, nil)
		res.Body.Close()
		require.Equal(t, http.StatusBadRequest, res.StatusCode, path)
	}

	hits := decodeLookup(t, h.do(t, http.MethodGet, lookupPath("bulk"), nil))
	require.Len(t, hits, 10, "a broad query is capped")

	// LIKE wildcards are text, not a way to match everyone.
	for _, q := range []string{"%%", "__", `\\`} {
		require.Empty(t, decodeLookup(t, h.do(t, http.MethodGet, lookupPath(q), nil)), "q=%q", q)
	}
}

// Who can be a requester: not an account that was deleted, not a service
// identity (an OAuth client's, which has no inbox and cannot be written to).
func TestUserLookup_LeavesOutDeletedAccountsAndServiceIdentities(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	gone, err := h.userSvc.Create(ctx, user.CreateUserInput{
		Email: "gone@test.local", DisplayName: "Zed Gone", Role: user.RoleUser, Password: "a-real-passphrase",
	})
	require.NoError(t, err)
	require.NoError(t, h.userSvc.SoftDelete(ctx, gone.ID))
	require.Empty(t, decodeLookup(t, h.do(t, http.MethodGet, lookupPath("Zed Gone"), nil)))

	// A service identity, built the way an OAuth client's is.
	_, err = h.tx.ExecContext(ctx, `INSERT INTO users (id, email, display_name, role, service_account)
		VALUES (gen_random_uuid(), 'svc-zed@test.local', 'Zed Service', 'staff', true)`)
	require.NoError(t, err)
	require.Empty(t, decodeLookup(t, h.do(t, http.MethodGet, lookupPath("Zed Service"), nil)))
}

// A disabled account cannot sign in, so it is not a sensible requester and is
// not offered (MCP create_ticket still accepts one by id; this is the picker).
// Disabled accounts do not crowd out the enabled ones: the answer is the first
// ten enabled matches, not the first ten matches with the disabled removed.
func TestUserLookup_LeavesOutDisabledAccounts(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	for i := range 20 {
		u, err := h.userSvc.Create(ctx, user.CreateUserInput{
			Email: fmt.Sprintf("crowd%02d@test.local", i), DisplayName: fmt.Sprintf("Crowd Person %02d", i),
			Role: user.RoleUser, Password: "a-real-passphrase",
		})
		require.NoError(t, err)
		if i >= 12 { // the newest, which the list puts first
			require.NoError(t, h.userSvc.Disable(ctx, u.ID))
		}
	}

	hits := decodeLookup(t, h.do(t, http.MethodGet, lookupPath("crowd"), nil))
	require.Len(t, hits, 10, "twelve are enabled, so the cap is reached")
	for _, hit := range hits {
		var n int
		_, err := fmt.Sscanf(hit.Email, "crowd%02d@", &n)
		require.NoError(t, err)
		require.Less(t, n, 12, "%s is disabled", hit.Email)
	}
}

// Most of the matches are disabled, and the answer is still up to ten enabled
// ones, once, in an order that does not depend on how the rows were read: the
// disabled are left out by the query itself, not by walking every match and
// dropping them, and the order (display name, then id) is a total one, so no
// account can be answered twice or missed. Created in name order, so
// "newest first" (the admin list's order) is the reverse of it and does not pass.
func TestUserLookup_MostlyDisabledMatchesStillAnswerTenInOrderWithoutRepeats(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	const total = 120
	for i := 0; i < total; i++ {
		_, err := h.userSvc.Create(ctx, user.CreateUserInput{
			Email: fmt.Sprintf("mass%03d@test.local", i), DisplayName: fmt.Sprintf("Mass Person %03d", i),
			Role: user.RoleUser, Password: "a-real-passphrase",
		})
		require.NoError(t, err)
	}
	// 14 enabled (every ninth), 106 disabled: the first ten enabled by name are
	// 000, 009, ... 081.
	_, err := h.tx.ExecContext(ctx, `UPDATE users SET disabled = true
		WHERE email LIKE 'mass%@test.local' AND (substring(email from 5 for 3))::int % 9 <> 0`)
	require.NoError(t, err)

	hits := decodeLookup(t, h.do(t, http.MethodGet, lookupPath("mass"), nil))

	var got []string
	seen := map[string]bool{}
	for _, hit := range hits {
		require.False(t, seen[hit.ID], "%s answered twice", hit.Email)
		seen[hit.ID] = true
		got = append(got, hit.Email)
	}
	var want []string
	for i := 0; len(want) < 10; i += 9 {
		want = append(want, fmt.Sprintf("mass%03d@test.local", i))
	}
	require.Equal(t, want, got, "the first ten enabled matches by display name")
}
