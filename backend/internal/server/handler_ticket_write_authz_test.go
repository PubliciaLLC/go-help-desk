package server_test

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
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
