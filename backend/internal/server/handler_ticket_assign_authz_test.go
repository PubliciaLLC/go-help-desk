package server_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// DESIGN.md gives assignment to Staff ("Assign tickets to any staff member or
// group") and not to User, whose row covers creating, viewing and updating
// their own tickets only.
//
// The ticket-subtree middleware only establishes that the caller may SEE the
// ticket. Assignment is a role question on top of that, and ticket.Service.Assign
// takes an actor purely to write the audit entry — it gates nothing. So a
// reporter could direct work to any staff member by PATCHing their own ticket.
func TestReportingUser_CannotAssignTheirOwnTicket(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsUser(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "my printer", "description": "jammed", "category_id": h.catID.String(),
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))

	// Their own ticket: the subtree guard lets them through, so this is purely
	// the role check being exercised.
	resp = h.doAsUser(t, http.MethodPatch, "/api/v1/tickets/"+created.ID, map[string]any{
		"assignee_user_id": h.staffID.String(),
	})
	require.Equal(t, http.StatusForbidden, resp.StatusCode,
		"a reporting user must not assign, even on their own ticket")

	// And it must not have happened anyway — a 403 that still wrote is worse
	// than an honest 200.
	resp = h.doAsUser(t, http.MethodGet, "/api/v1/tickets/"+created.ID, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got struct {
		AssigneeUserID *string `json:"assignee_user_id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Nil(t, got.AssigneeUserID, "the refused assignment must not have been written")
}

func TestReportingUser_CannotClearAssignment(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsUser(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "unassign me", "description": "x", "category_id": h.catID.String(),
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))

	// Staff assigns it legitimately.
	resp = h.do(t, http.MethodPatch, "/api/v1/tickets/"+created.ID, map[string]any{
		"assignee_user_id": h.staffID.String(),
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// clear_assignee is a separate branch in the handler from assignee_user_id,
	// so guarding only the latter would leave the reporter able to unassign the
	// staff member working their ticket.
	resp = h.doAsUser(t, http.MethodPatch, "/api/v1/tickets/"+created.ID, map[string]any{
		"clear_assignee": true,
	})
	require.Equal(t, http.StatusForbidden, resp.StatusCode,
		"a reporting user must not clear an assignment either")
}

// The guard must not break the people who are supposed to assign.
func TestStaffAndAdmin_CanStillAssign(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	for _, tc := range []struct {
		name string
		do   func(*testing.T, string, string, any) *http.Response
	}{
		{"staff", h.do},
		{"admin", h.doAsAdmin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := h.doAsUser(t, http.MethodPost, "/api/v1/tickets", map[string]any{
				"subject": "assignable " + tc.name, "description": "x", "category_id": h.catID.String(),
			})
			require.Equal(t, http.StatusCreated, resp.StatusCode)
			var created struct {
				ID string `json:"id"`
			}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))

			resp = tc.do(t, http.MethodPatch, "/api/v1/tickets/"+created.ID, map[string]any{
				"assignee_user_id": h.staffID.String(),
			})
			require.Equal(t, http.StatusOK, resp.StatusCode)

			resp = tc.do(t, http.MethodPatch, "/api/v1/tickets/"+created.ID, map[string]any{
				"clear_assignee": true,
			})
			require.Equal(t, http.StatusOK, resp.StatusCode)
		})
	}
}

// Routing rules auto-assign on create through ticket.SystemActor, which carries
// RoleAdmin. A role check placed in the domain service rather than the handler
// would still pass here — but a check placed anywhere that inspects the HTTP
// caller's role would break auto-assignment for tickets filed by reporters.
func TestAutoAssignmentOnCreate_StillWorksForReporters(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsUser(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "routed", "description": "x", "category_id": h.catID.String(),
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode,
		"a reporter filing a ticket must not be blocked by the assignment guard")
}

// ticket.ErrForbidden reached handleError, which mapped only NotFound and sent
// everything else to 500. A reporter calling resolve therefore got "an internal
// error occurred" for what is an ordinary, correct refusal — it reads as a bug
// in the server and hides a real permission boundary from anyone reading logs.
func TestReportingUser_ResolveIsForbiddenNotServerError(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsUser(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject": "resolve me", "description": "x", "category_id": h.catID.String(),
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))

	resp = h.doAsUser(t, http.MethodPost,
		fmt.Sprintf("/api/v1/tickets/%s/resolve", created.ID), map[string]any{})
	require.Equal(t, http.StatusForbidden, resp.StatusCode,
		"a refused transition is a 403, not a 500")

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, "forbidden", body.Error.Code,
		"the code must say forbidden, not internal_error")
}
