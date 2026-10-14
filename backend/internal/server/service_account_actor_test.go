package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
)

// An OAuth client's actor carries its service user's id and stored role (#420).
// What follows is what that does and does not open up.

// GET /me answers a client in the shape it always did. The actor has a user id
// now, and the route must not start handing back a user object because of it.
func TestOAuthClient_MeKeepsTheMachineShape(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c := h.newServiceClient(t, "Nagios", "tickets:read")

	resp := h.doWithBearer(t, c.Token, "/api/v1/me")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Equal(t, true, got["machine"])
	require.Equal(t, "staff", got["role"])
	require.Equal(t, []any{"tickets:read"}, got["scopes"])
	require.NotContains(t, got, "email", "a client is not shown as a user")
	require.NotContains(t, got, "id")
}

// Giving the actor a real user id must not open the routes that change how an
// account signs in. DenyMachineCredentials keys on Machine, which stays true.
func TestOAuthClient_StillRefusedTheSignInRoutes(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c := h.newServiceClient(t, "Nagios", "tickets:read", "tickets:write")

	cases := []struct{ name, method, path string }{
		{"change password", http.MethodPatch, "/api/v1/me/password"},
		{"start MFA enrolment", http.MethodPost, "/api/v1/me/mfa/enroll"},
		{"confirm MFA enrolment", http.MethodPost, "/api/v1/me/mfa/enroll/confirm"},
		{"list passkeys", http.MethodGet, "/api/v1/me/passkeys"},
		{"start passkey registration", http.MethodPost, "/api/v1/me/passkeys/register/start"},
		{"finish passkey registration", http.MethodPost, "/api/v1/me/passkeys/register/finish"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := bearerRequest(t, h, c.Token, tc.method, tc.path, map[string]any{})
			defer res.Body.Close()
			require.Equal(t, http.StatusForbidden, res.StatusCode)
			require.Equal(t, "session_required", errorCode(t, res))
		})
	}
}

// Ticket scope (#391) follows the service user: no change to the rules, they
// key on the actor's user id and role, which a client now has. The two ends are
// pinned here: with enforcement off a group-less client sees what it always
// saw, and with it on the client sees what its user's groups cover and nothing
// else, on every surface that applies scope.
func TestOAuthClient_ScopeEnforcementFollowsItsServiceUser(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	otherCat, err := h.categorySvc.CreateCategory(ctx, "Other category", 2)
	require.NoError(t, err)
	inScope := createTicketID(t, h, "Covered by the client's group")
	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "Not covered", "description": "x", "category_id": otherCat.ID.String(), "priority": "low",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var other struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&other))
	outOfScope := other.ID

	c := h.newServiceClient(t, "Nagios", "tickets:read", "audit:read")

	listIDs := func(t *testing.T) []uuid.UUID {
		t.Helper()
		res := h.doWithBearer(t, c.Token, "/api/v1/tickets?limit=200")
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
		var rows []struct {
			ID uuid.UUID `json:"id"`
		}
		require.NoError(t, json.NewDecoder(res.Body).Decode(&rows))
		out := make([]uuid.UUID, len(rows))
		for i, r := range rows {
			out[i] = r.ID
		}
		return out
	}
	statusOf := func(id uuid.UUID) int {
		res := h.doWithBearer(t, c.Token, "/api/v1/tickets/"+id.String())
		res.Body.Close()
		return res.StatusCode
	}
	auditedTickets := func(t *testing.T) []uuid.UUID {
		t.Helper()
		res := h.doWithBearer(t, c.Token, "/api/v1/admin/audit?limit=500")
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
		var page struct {
			Entries []struct {
				EntityType string    `json:"entity_type"`
				EntityID   uuid.UUID `json:"entity_id"`
			} `json:"entries"`
		}
		require.NoError(t, json.NewDecoder(res.Body).Decode(&page))
		var out []uuid.UUID
		for _, e := range page.Entries {
			if e.EntityType == "ticket" {
				out = append(out, e.EntityID)
			}
		}
		return out
	}

	t.Run("enforcement off: a group-less client sees what it always did", func(t *testing.T) {
		// By id, any ticket: staff are not scoped while enforcement is off.
		require.Equal(t, http.StatusOK, statusOf(inScope))
		require.Equal(t, http.StatusOK, statusOf(outOfScope))
		// The default list with enforcement off is staff's assigned queue, and
		// these tickets are assigned to nobody; that is how it was for a client
		// before it had a user, too.
		require.Empty(t, listIDs(t))
	})

	enableScope(t, h)

	t.Run("enforcement on, no groups: nothing", func(t *testing.T) {
		require.NotContains(t, listIDs(t), inScope)
		require.NotContains(t, listIDs(t), outOfScope)
		require.Equal(t, http.StatusNotFound, statusOf(inScope))
	})

	g, err := h.groupSvc.Create(ctx, "Monitoring", "")
	require.NoError(t, err)
	require.NoError(t, h.groupSvc.AddMember(ctx, g.ID, c.UserID))
	require.NoError(t, h.groupSvc.AddScope(ctx, group.GroupScope{GroupID: g.ID, CategoryID: h.catID}))

	t.Run("enforcement on, its user in a group: that group's scope", func(t *testing.T) {
		require.Contains(t, listIDs(t), inScope)
		require.NotContains(t, listIDs(t), outOfScope)
		require.Equal(t, http.StatusOK, statusOf(inScope))
		require.Equal(t, http.StatusNotFound, statusOf(outOfScope), "in and out of scope by id")
	})

	t.Run("the group filter is held to the same scope", func(t *testing.T) {
		stranger, err := h.groupSvc.Create(ctx, "Legal", "")
		require.NoError(t, err)
		res := h.doWithBearer(t, c.Token, "/api/v1/tickets?assignee_group_id="+stranger.ID.String())
		res.Body.Close()
		require.Equal(t, http.StatusForbidden, res.StatusCode)
		res = h.doWithBearer(t, c.Token, "/api/v1/tickets?assignee_group_id="+g.ID.String())
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
	})

	t.Run("the audit view shows only tickets in scope", func(t *testing.T) {
		seen := auditedTickets(t)
		require.Contains(t, seen, inScope)
		require.NotContains(t, seen, outOfScope)
	})

	t.Run("status counts count the client's tickets", func(t *testing.T) {
		// Give the in-scope ticket to the client's group: counts are by
		// assignee or group, which a client with no user id could never match.
		res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+inScope.String(),
			map[string]any{"assignee_group_id": g.ID.String()})
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)

		res = h.doWithBearer(t, c.Token, "/api/v1/statuses")
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
		var statuses []struct {
			TicketCount int64 `json:"ticket_count"`
		}
		require.NoError(t, json.NewDecoder(res.Body).Decode(&statuses))
		var total int64
		for _, s := range statuses {
			total += s.TicketCount
		}
		require.Equal(t, int64(1), total)
	})
}
