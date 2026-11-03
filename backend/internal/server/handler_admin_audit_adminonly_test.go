package server_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

// The admin-wide audit view is administrators only (Erik, 2026-11-03: "they
// can see the history on a ticket but not the whole audit log"). It used to
// be open to staff, narrowed to ticket entries in their scope (#129, #331).
// A staff member's window onto one ticket is the per-ticket history, which is
// unchanged.

const adminAuditPath = "/api/v1/admin/audit"

// seedStaffKey stores a key owned by the staff user straight in the store, so
// it can hold a scope the API would now refuse to put on a staff key: that is
// the shape of one issued before this change.
func seedStaffKey(t *testing.T, h *harness, scopes ...string) string {
	t.Helper()
	raw, _, err := auth.GenerateToken()
	require.NoError(t, err)
	raw = "GHD_" + raw
	require.NoError(t, h.authStore.CreateAPIKey(context.Background(), auth.APIKey{
		ID: uuid.New(), Name: "older staff key", HashedToken: auth.HashToken(raw), UserID: h.staffID,
		Scopes: scopes, CreatedAt: time.Now(),
	}))
	return raw
}

func TestAdminAudit_RefusesStaffOnEveryCredential(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	// A ticket with history, so a refusal is not an empty page in disguise.
	createAndResolveTicket(t, h)

	cases := []struct {
		name string
		do   func() *http.Response
	}{
		{"session", func() *http.Response {
			return h.doSession(t, h.session(t, asStaff), http.MethodGet, adminAuditPath, nil)
		}},
		{"API key", func() *http.Response { return h.do(t, http.MethodGet, adminAuditPath, nil) }},
		{"API key carrying audit:read", func() *http.Response {
			return requestWithKey(t, h, seedStaffKey(t, h, "tickets:read", "audit:read"), adminAuditPath)
		}},
		{"OAuth client carrying audit:read", func() *http.Response {
			c := h.newServiceClient(t, "Nagios", "tickets:read", "audit:read")
			return h.doWithBearer(t, c.Token, adminAuditPath)
		}},
		{"OAuth client without audit:read", func() *http.Response {
			c := h.newServiceClient(t, "Nagios", "tickets:read")
			return h.doWithBearer(t, c.Token, adminAuditPath)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := tc.do()
			b := readAll(t, res)
			require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", b)
			require.NotContains(t, string(b), `"entries"`)
		})
	}
}

func TestAdminAudit_StillOpenToAdministrators(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	createAndResolveTicket(t, h)

	cases := []struct {
		name string
		do   func() *http.Response
	}{
		{"session", func() *http.Response {
			return h.doSession(t, h.session(t, asAdmin), http.MethodGet, adminAuditPath, nil)
		}},
		{"API key", func() *http.Response { return h.doAsAdmin(t, http.MethodGet, adminAuditPath, nil) }},
		{"API key with audit:read", func() *http.Response {
			return requestWithKey(t, h, mintKey(t, h, []string{"audit:read"}), adminAuditPath)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := tc.do()
			b := readAll(t, res)
			require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)
			require.Contains(t, string(b), `"entries"`)
		})
	}
}

// What staff keep: the history on a ticket they can see.
func TestAdminAudit_StaffKeepTheirTicketsHistory(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := createAndResolveTicket(t, h)

	for _, path := range []string{"/audit", "/history"} {
		for name, do := range map[string]func() *http.Response{
			"session": func() *http.Response {
				return h.doSession(t, h.session(t, asStaff), http.MethodGet, "/api/v1/tickets/"+id+path, nil)
			},
			"API key": func() *http.Response { return h.do(t, http.MethodGet, "/api/v1/tickets/"+id+path, nil) },
		} {
			res := do()
			b := readAll(t, res)
			require.Equal(t, http.StatusOK, res.StatusCode, "%s %s: %s", name, path, b)
		}
	}
}

// audit:read opens nothing for staff now, so a key owned by staff cannot be
// given it: the same refusal every other administrator-only scope gets.
func TestAdminAudit_StaffKeyCannotBeIssuedAuditRead(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	h.allowStaffKeys(t)

	res := h.doSession(t, h.session(t, asStaff), http.MethodPost, meKeys, map[string]any{
		"name": "mine", "scopes": []string{"audit:read"}, "expires_at": inYears(1),
	})
	b := readAll(t, res)
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", b)

	res, b = h.issueFor(t, h.session(t, asAdmin), h.staffID, "audit:read")
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", b)

	// An administrator's key still can.
	res, b = h.issueFor(t, h.session(t, asAdmin), h.adminID, "audit:read")
	require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", b)
}

// What the staff-scoped tests used to prove from the other side: an
// administrator sees every entity type, and ticket scope never narrows them,
// with enforcement on or off.
func TestAdminAudit_AdministratorsSeeEveryEntityAndAreNeverScoped(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "not the staff user's business", "category_id": h.catID.String(), "priority": "low",
	})
	var created struct {
		ID string `json:"id"`
	}
	decodeJSON(t, resp, &created)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.NoError(t, h.userSvc.ResetMFA(ctx, h.staffID, nil))

	for _, enforced := range []bool{false, true} {
		if enforced {
			enableScope(t, h)
		}
		resp := h.doAsAdmin(t, http.MethodGet, adminAuditPath+"?limit=200", nil)
		var got adminAuditResponse
		decodeJSON(t, resp, &got)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var sawTicket, sawUser bool
		for _, e := range got.Entries {
			sawTicket = sawTicket || e.EntityID == created.ID
			sawUser = sawUser || e.EntityType == "user"
		}
		require.True(t, sawTicket, "enforced=%v: the out-of-scope ticket's entry", enforced)
		require.True(t, sawUser, "enforced=%v: the mfa_reset entry on a user", enforced)
	}
}
