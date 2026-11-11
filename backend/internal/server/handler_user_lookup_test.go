package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"
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

// staffLookup is a GET /users/lookup on a signed-in staff session.
func staffLookup(t *testing.T, h *harness, path string) *http.Response {
	t.Helper()
	return h.doSession(t, h.session(t, asStaff), http.MethodGet, path, nil)
}

// lookupAsSession is the same for a chosen role, shaped like harness.do.
func lookupAsSession(h *harness, w who) func(*testing.T, string, string, any) *http.Response {
	return func(t *testing.T, method, path string, body any) *http.Response {
		t.Helper()
		return h.doSession(t, h.session(t, w), method, path, body)
	}
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
			// The picker is a staff-session feature (#290): a key is refused,
			// see TestUserLookup_RefusesMachineCredentials.
			do := lookupAsSession(h, asStaff)
			if caller == "admin" {
				do = lookupAsSession(h, asAdmin)
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

	res := staffLookup(t, h, lookupPath("Reporting"))
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

// #290, Erik's ruling: there is no user lookup endpoint for machine credentials,
// so there is no way to enumerate accounts with a key. users:read is no help:
// staff keys carry it by default. An API key and an OAuth client are both
// refused with 403 session_required, and a staff session still gets 200.
func TestUserLookup_RefusesMachineCredentials(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	client := h.newOverScopedServiceClient(t, "Directory scraper", "users:read")
	cases := []struct {
		name string
		res  func() *http.Response
	}{
		{"staff key with every default scope", func() *http.Response {
			return withKey(t, h, h.apiKey, http.MethodGet, lookupPath("Reporting"), nil)
		}},
		{"admin key", func() *http.Response {
			return withKey(t, h, h.adminKey, http.MethodGet, lookupPath("Reporting"), nil)
		}},
		{"key with users:read", func() *http.Response {
			return withKey(t, h, mintKey(t, h, []string{"users:read"}), http.MethodGet, lookupPath("Reporting"), nil)
		}},
		{"OAuth client with users:read", func() *http.Response {
			return h.doBearer(t, client.Token, http.MethodGet, lookupPath("Reporting"), nil)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := tc.res()
			body := readBody(t, res)
			require.Equal(t, http.StatusForbidden, res.StatusCode, body)
			require.Contains(t, body, "session_required")
			require.NotContains(t, body, "user@test.local", "the refusal leaks nothing")
		})
	}

	res := staffLookup(t, h, lookupPath("Reporting"))
	require.Equal(t, http.StatusOK, res.StatusCode, readBody(t, res))
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
		res := staffLookup(t, h, path)
		res.Body.Close()
		require.Equal(t, http.StatusBadRequest, res.StatusCode, path)
	}

	hits := decodeLookup(t, staffLookup(t, h, lookupPath("bulk")))
	require.Len(t, hits, 10, "a broad query is capped")

	// LIKE wildcards are text, not a way to match everyone.
	for _, q := range []string{"%%", "__", `\\`} {
		require.Empty(t, decodeLookup(t, staffLookup(t, h, lookupPath(q))), "q=%q", q)
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
	require.Empty(t, decodeLookup(t, staffLookup(t, h, lookupPath("Zed Gone"))))

	// A service identity, built the way an OAuth client's is.
	_, err = h.tx.ExecContext(ctx, `INSERT INTO users (id, email, display_name, role, service_account)
		VALUES (gen_random_uuid(), 'svc-zed@test.local', 'Zed Service', 'staff', true)`)
	require.NoError(t, err)
	require.Empty(t, decodeLookup(t, staffLookup(t, h, lookupPath("Zed Service"))))
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

	hits := decodeLookup(t, staffLookup(t, h, lookupPath("crowd")))
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

	hits := decodeLookup(t, staffLookup(t, h, lookupPath("mass")))

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

// GET /users/lookup?id= (advanced search, PR D): the ticket list's requester
// chip turns the account id in its URL back into a name after a reload. It is
// the same lookup as ?q= with the same rules, so these tests pin the ways it
// must not differ: session only, same people left out, same three fields.

func lookupByIDPath(id string) string { return "/api/v1/users/lookup?id=" + url.QueryEscape(id) }

func TestUserLookupByID_StaffAndAdminGetTheAccount(t *testing.T) {
	for name, caller := range map[string]who{"staff": asStaff, "admin": asAdmin} {
		t.Run(name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			res := h.doSession(t, h.session(t, caller), http.MethodGet, lookupByIDPath(h.userID.String()), nil)
			body := readBody(t, res)
			require.Equal(t, http.StatusOK, res.StatusCode, body)
			// A single object holding the picker's three fields and nothing else.
			var row map[string]any
			require.NoError(t, json.Unmarshal([]byte(body), &row))
			require.Equal(t, map[string]any{
				"id": h.userID.String(), "display_name": "Reporting User", "email": "user@test.local",
			}, row)
		})
	}
}

func TestUserLookupByID_RequesterAndAnonymousAreRefused(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.doAsUser(t, http.MethodGet, lookupByIDPath(h.userID.String()), nil)
	body := readBody(t, res)
	require.Equal(t, http.StatusForbidden, res.StatusCode, body)
	require.NotContains(t, body, "user@test.local", "the refusal leaks nothing")

	res = h.doUnauth(t, http.MethodGet, lookupByIDPath(h.userID.String()), nil)
	res.Body.Close()
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
}

// Same ruling as ?q= (#290): no key or OAuth client resolves an id into a name
// and an address, whatever scopes it holds, so ids cannot be walked either.
func TestUserLookupByID_RefusesMachineCredentials(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	client := h.newOverScopedServiceClient(t, "Directory scraper", "users:read")
	path := lookupByIDPath(h.userID.String())
	cases := []struct {
		name string
		res  func() *http.Response
	}{
		{"staff key with every default scope", func() *http.Response {
			return withKey(t, h, h.apiKey, http.MethodGet, path, nil)
		}},
		{"admin key", func() *http.Response {
			return withKey(t, h, h.adminKey, http.MethodGet, path, nil)
		}},
		{"key with users:read", func() *http.Response {
			return withKey(t, h, mintKey(t, h, []string{"users:read"}), http.MethodGet, path, nil)
		}},
		{"OAuth client with users:read", func() *http.Response {
			return h.doBearer(t, client.Token, http.MethodGet, path, nil)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := tc.res()
			body := readBody(t, res)
			require.Equal(t, http.StatusForbidden, res.StatusCode, body)
			require.Contains(t, body, "session_required")
			require.NotContains(t, body, "user@test.local", "the refusal leaks nothing")
		})
	}
}

// An id that names nobody, a deleted account, a disabled one and a service
// identity all answer the same 404: the lookup must not tell a caller which
// ids are real accounts that happen to be excluded.
func TestUserLookupByID_UnknownAndExcludedAccountsAreTheSame404(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	mk := func(email, name string) uuid.UUID {
		u, err := h.userSvc.Create(ctx, user.CreateUserInput{
			Email: email, DisplayName: name, Role: user.RoleUser, Password: "a-real-passphrase",
		})
		require.NoError(t, err)
		return u.ID
	}
	deleted := mk("gone-byid@test.local", "Gone ById")
	require.NoError(t, h.userSvc.SoftDelete(ctx, deleted))
	disabled := mk("off-byid@test.local", "Off ById")
	require.NoError(t, h.userSvc.Disable(ctx, disabled))
	service := uuid.New()
	_, err := h.tx.ExecContext(ctx, `INSERT INTO users (id, email, display_name, role, service_account)
		VALUES ($1, 'svc-byid@test.local', 'Svc ById', 'staff', true)`, service)
	require.NoError(t, err)

	var want string
	for name, id := range map[string]uuid.UUID{
		"unknown": uuid.New(), "deleted": deleted, "disabled": disabled, "service identity": service,
	} {
		res := staffLookup(t, h, lookupByIDPath(id.String()))
		body := readBody(t, res)
		require.Equal(t, http.StatusNotFound, res.StatusCode, "%s: %s", name, body)
		require.NotContains(t, body, "byid@test.local", "%s: the refusal leaks nothing", name)
		if want == "" {
			want = body
		}
		require.Equal(t, want, body, "%s answers differently from the others", name)
	}
}

// Not an id, or an id with a query beside it: a mistake in the caller, said so
// rather than guessed at.
func TestUserLookupByID_RejectsMalformedRequests(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	for _, path := range []string{
		"/api/v1/users/lookup?id=",
		lookupByIDPath("not-a-uuid"),
		lookupByIDPath("Reporting"),
		lookupByIDPath(h.userID.String()) + "&q=Reporting",
	} {
		res := staffLookup(t, h, path)
		res.Body.Close()
		require.Equal(t, http.StatusBadRequest, res.StatusCode, path)
	}
}
