package server_test

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Visibility was enforced on the ticket READ handler and silently omitted on
// the write handlers. A reporting user could therefore post a reply to any
// ticket by UUID — including one it was refused permission to read — and
// because replies default to notify_customer, the text was mailed to the real
// reporter. Verified before the fix: the same account and ticket answered 403
// on GET and 201 on POST.
//
// These tests pin both halves: the write is refused, and the legitimate case
// still works.

func TestWritePathsEnforceTicketVisibility(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	// Reported by the ADMIN. The seeded reporting user has no relationship to
	// it: not reporter, not assignee, not in an assigned group.
	foreign, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject:        "Payroll discrepancy",
		Description:    "Contains salary figures",
		CategoryID:     h.catID,
		Priority:       ticket.PriorityHigh,
		ReporterUserID: &h.adminID,
	})
	require.NoError(t, err)

	// Baseline: the read is refused, so any accepted write is unambiguously a gap.
	res := h.doAsUser(t, http.MethodGet, "/api/v1/tickets/"+foreign.ID.String(), nil)
	res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode, "precondition: the read is gated")

	t.Run("reply is refused", func(t *testing.T) {
		res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+foreign.ID.String()+"/replies",
			map[string]any{"body": "injected by an unrelated user"})
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		require.Equal(t, http.StatusForbidden, res.StatusCode,
			"a user that cannot read a ticket must not write to it; body %s", b)
	})

	t.Run("update is refused", func(t *testing.T) {
		res := h.doAsUser(t, http.MethodPatch, "/api/v1/tickets/"+foreign.ID.String(),
			map[string]any{"assignee_user_id": h.staffID.String()})
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		require.Equal(t, http.StatusForbidden, res.StatusCode,
			"reassigning a foreign ticket must be refused; body %s", b)
	})

	t.Run("no reply was persisted", func(t *testing.T) {
		replies, err := h.ticketSvc.ListReplies(ctx, foreign.ID)
		require.NoError(t, err)
		require.Empty(t, replies, "the refused write must not have reached the store")
	})
}

// The fix must not break the ordinary case it sits in front of.
func TestReporterCanStillReplyToTheirOwnTicket(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	userID := h.userID
	own, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject:        "My laptop will not boot",
		CategoryID:     h.catID,
		Priority:       ticket.PriorityMedium,
		ReporterUserID: &userID,
	})
	require.NoError(t, err)

	res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+own.ID.String()+"/replies",
		map[string]any{"body": "Still broken this morning"})
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode,
		"a reporter must still be able to reply on their own ticket; body %s", b)
}

// "Clear assignment" used to send {assignee_user_id: undefined,
// assignee_group_id: undefined}, which JSON.stringify serialises to {} — so
// the handler's assign branch never ran, the request returned 200, and the
// ticket kept its assignee with no error shown. The two fields cannot express
// "nobody" on their own: the server decodes an explicit null and an omitted
// key into the same nil pointer.
func TestUpdateTicket_ClearAssignee(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject:        "Assigned then released",
		CategoryID:     h.catID,
		Priority:       ticket.PriorityMedium,
		ReporterUserID: &h.adminID,
	})
	require.NoError(t, err)

	_, err = h.ticketSvc.Assign(ctx, tk.ID, &h.staffID, nil,
		ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin})
	require.NoError(t, err)

	assigned, err := h.ticketSvc.GetByID(ctx, tk.ID)
	require.NoError(t, err)
	require.NotNil(t, assigned.AssigneeUserID, "precondition: the ticket is assigned")

	t.Run("an empty body still changes nothing", func(t *testing.T) {
		res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(), map[string]any{})
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)

		got, err := h.ticketSvc.GetByID(ctx, tk.ID)
		require.NoError(t, err)
		require.NotNil(t, got.AssigneeUserID,
			"an empty patch must not silently unassign — that is why the flag exists")
	})

	t.Run("clear_assignee unassigns", func(t *testing.T) {
		res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+tk.ID.String(),
			map[string]any{"clear_assignee": true})
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)

		got, err := h.ticketSvc.GetByID(ctx, tk.ID)
		require.NoError(t, err)
		require.Nil(t, got.AssigneeUserID, "the ticket must actually be unassigned")
		require.Nil(t, got.AssigneeGroupID)
	})
}
