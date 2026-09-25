package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// A staff member can read the list of colleagues and give a ticket to one.
//
// This is the whole journey the assignee panel makes, and it has been broken
// twice in three rounds without anybody noticing: first because staff could
// read no list of users at all, so the picker was empty; then because the
// endpoint that fixed it was registered on the wrong router and answered 404
// to everyone, administrators included. Both times every gate was green,
// because nothing exercised the two halves together.
//
// So: read the list as staff, pick somebody out of it, assign, and read the
// ticket back. If any link in that breaks, this fails.
func TestStaff_CanReadTheListAndAssignFromIt(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Needs an owner", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.userID,
	})
	require.NoError(t, err)

	// The list, as staff. h.do authenticates as the seeded staff user.
	list := h.do(t, http.MethodGet, "/api/v1/staff", nil)
	defer list.Body.Close()
	require.Equal(t, http.StatusOK, list.StatusCode,
		"staff cannot read the list of people they are meant to assign work to")

	var staff []struct {
		ID         string `json:"id"`
		Name       string `json:"display_name"`
		Assignable bool   `json:"assignable"`
	}
	require.NoError(t, json.NewDecoder(list.Body).Decode(&staff))

	var pick string
	for _, s := range staff {
		if s.Assignable {
			pick = s.ID
			break
		}
	}
	require.NotEmpty(t, pick, "the list contains nobody who can be given work")

	// Assign to them, as staff.
	res := h.do(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(),
		map[string]any{"assignee_user_id": pick})
	body, _ := readAllBody(res)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode,
		"staff were refused an assignment the design says they may make: %s", body)

	// And it stuck.
	after, err := h.ticketSvc.GetByID(ctx, tk.ID)
	require.NoError(t, err)
	require.NotNil(t, after.AssigneeUserID, "the assignment did not take")
	require.Equal(t, pick, after.AssigneeUserID.String())
}

// A reporting user cannot read the list, and cannot assign.
func TestReportingUser_CannotReadTheListOrAssign(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Mine", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.userID,
	})
	require.NoError(t, err)

	list := h.doAsUser(t, http.MethodGet, "/api/v1/staff", nil)
	list.Body.Close()
	require.Equal(t, http.StatusForbidden, list.StatusCode)

	res := h.doAsUser(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(),
		map[string]any{"assignee_user_id": h.staffID.String()})
	res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode,
		"a reporter assigned their own ticket to somebody")
}
