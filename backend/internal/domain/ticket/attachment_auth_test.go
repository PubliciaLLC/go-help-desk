package ticket_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// CanUploadAttachment and CanGuestUploadAttachment (#315, found by an
// adversarial review of #312): handleUploadAttachment checked ownership and
// nothing else, so a reporter could attach a file to their own Closed
// ticket even though the equivalent reply was already refused. These reuse
// addReply's own authorization rule — CanUserUpdate / CanGuestUpdate —
// rather than a second copy of it, so the two can never drift apart again.

func (h *harness) seedOpenTicket(reporterID uuid.UUID) ticket.Ticket {
	t := ticket.Ticket{
		ID:             uuid.New(),
		TrackingNumber: "HD-100001",
		Subject:        "Needs a screenshot",
		ReporterUserID: &reporterID,
		StatusID:       h.newStatus.ID,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	h.store.seed(t)
	return t
}

func TestCanUploadAttachment_ReporterMustOwnTheTicket(t *testing.T) {
	h := newHarness(t)
	owner := uuid.New()
	stranger := uuid.New()
	tk := h.seedOpenTicket(owner)

	require.NoError(t, h.svc.CanUploadAttachment(context.Background(), tk.ID,
		ticket.Actor{UserID: ptr(owner), Role: user.RoleUser}, 7))

	err := h.svc.CanUploadAttachment(context.Background(), tk.ID,
		ticket.Actor{UserID: ptr(stranger), Role: user.RoleUser}, 7)
	require.ErrorIs(t, err, ticket.ErrForbidden)
}

func TestCanUploadAttachment_StaffAndAdminBypassOwnership(t *testing.T) {
	h := newHarness(t)
	owner := uuid.New()
	tk := h.seedOpenTicket(owner)

	for _, role := range []user.Role{user.RoleStaff, user.RoleAdmin} {
		err := h.svc.CanUploadAttachment(context.Background(), tk.ID,
			ticket.Actor{UserID: ptr(uuid.New()), Role: role}, 7)
		require.NoError(t, err, "role %s must not need to own the ticket", role)
	}
}

func TestCanUploadAttachment_ObeysTheLifecycle(t *testing.T) {
	owner := uuid.New()
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}

	t.Run("refused on a closed ticket", func(t *testing.T) {
		h := newHarness(t)
		tk := h.seedOpenTicket(owner)
		require.NoError(t, h.svc.Close(context.Background(), tk.ID, staff))

		err := h.svc.CanUploadAttachment(context.Background(), tk.ID,
			ticket.Actor{UserID: ptr(owner), Role: user.RoleUser}, 7)
		require.ErrorIs(t, err, ticket.ErrClosed)
	})

	t.Run("allowed on a resolved ticket inside the reopen window", func(t *testing.T) {
		h := newHarness(t)
		tk := h.seedResolved(owner)

		err := h.svc.CanUploadAttachment(context.Background(), tk.ID,
			ticket.Actor{UserID: ptr(owner), Role: user.RoleUser}, 7)
		require.NoError(t, err)
	})

	t.Run("refused on a resolved ticket past the reopen window", func(t *testing.T) {
		h := newHarness(t)
		tk := h.seedResolved(owner)

		err := h.svc.CanUploadAttachment(context.Background(), tk.ID,
			ticket.Actor{UserID: ptr(owner), Role: user.RoleUser}, 0)
		require.ErrorIs(t, err, ticket.ErrReopenWindowClosed)
	})

	t.Run("staff may still attach to a closed ticket", func(t *testing.T) {
		h := newHarness(t)
		tk := h.seedOpenTicket(owner)
		require.NoError(t, h.svc.Close(context.Background(), tk.ID, staff))

		err := h.svc.CanUploadAttachment(context.Background(), tk.ID, staff, 7)
		require.NoError(t, err, "staff and admin are exempt from the lifecycle rule, same as a reply")
	})
}

func TestCanGuestUploadAttachment_HasNoOwnershipCheckButObeysTheLifecycle(t *testing.T) {
	owner := uuid.New()
	staff := ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}

	t.Run("a guest with no reporter account may still attach", func(t *testing.T) {
		h := newHarness(t)
		email := "guest@example.test"
		tk, err := h.svc.Create(context.Background(), ticket.CreateInput{
			Subject: "No account", Description: "guest ticket", CategoryID: uuid.New(),
			GuestEmail: &email, GuestName: "Ada",
		})
		require.NoError(t, err)

		require.NoError(t, h.svc.CanGuestUploadAttachment(context.Background(), tk.ID, 7))
	})

	t.Run("refused on a closed ticket", func(t *testing.T) {
		h := newHarness(t)
		tk := h.seedOpenTicket(owner)
		require.NoError(t, h.svc.Close(context.Background(), tk.ID, staff))

		err := h.svc.CanGuestUploadAttachment(context.Background(), tk.ID, 7)
		require.ErrorIs(t, err, ticket.ErrClosed)
	})

	t.Run("allowed on a resolved ticket inside the reopen window", func(t *testing.T) {
		h := newHarness(t)
		tk := h.seedResolved(owner)

		require.NoError(t, h.svc.CanGuestUploadAttachment(context.Background(), tk.ID, 7))
	})

	t.Run("refused on a resolved ticket past the reopen window", func(t *testing.T) {
		h := newHarness(t)
		tk := h.seedResolved(owner)

		err := h.svc.CanGuestUploadAttachment(context.Background(), tk.ID, 0)
		require.ErrorIs(t, err, ticket.ErrReopenWindowClosed)
	})
}
