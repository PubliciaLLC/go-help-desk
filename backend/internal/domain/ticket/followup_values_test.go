package ticket_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// #625 item 8 (D24): a follow-up carries its original's custom field values.
// The copy is a hook the server wires, called after the follow-up commits, so
// the domain stays free of the custom field package: this pins when it runs and
// for whom.

type copyCall struct {
	from, to  ticket.Ticket
	requester bool
}

func recordCopies(h *harness) *[]copyCall {
	var calls []copyCall
	h.svc.SetFollowUpValueCopier(func(_ context.Context, from, to ticket.Ticket, requester bool) {
		calls = append(calls, copyCall{from, to, requester})
	})
	return &calls
}

func guestClosed(t *testing.T, h *harness) ticket.Ticket {
	t.Helper()
	tk, _ := guestTicket(t, h)
	require.NoError(t, h.svc.Close(context.Background(), tk.ID, ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff}))
	return tk
}

func TestFollowUp_StaffFollowUpAsksForItsOriginalsValues(t *testing.T) {
	h := newHarness(t)
	calls := recordCopies(h)
	orig := closedWithEverything(t, h)

	got, err := h.svc.CreateFollowUp(context.Background(), orig.ID, "GHD", ticket.ChannelWeb,
		ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff})
	require.NoError(t, err)

	require.Len(t, *calls, 1)
	require.Equal(t, orig.ID, (*calls)[0].from.ID)
	require.Equal(t, got.ID, (*calls)[0].to.ID, "the new ticket, as committed")
	require.Equal(t, orig.CategoryID, (*calls)[0].to.CategoryID)
	require.Equal(t, orig.TypeID, (*calls)[0].to.TypeID)
	require.Equal(t, orig.ItemID, (*calls)[0].to.ItemID)
	require.False(t, (*calls)[0].requester, "staff: every field that resolves")
}

func TestFollowUp_RequesterFollowUpsAskAsARequester(t *testing.T) {
	for name, actor := range map[string]func(ticket.Ticket) ticket.Actor{
		"an account holder": reporterActor,
		"a guest":           func(ticket.Ticket) ticket.Actor { return guestActor },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			calls := recordCopies(h)
			orig := closedWithEverything(t, h)
			if name == "a guest" {
				orig = guestClosed(t, h)
			}
			_, err := h.svc.CreateRequesterFollowUp(context.Background(), orig.ID, "GHD", ticket.ChannelWeb, actor(orig), openAlways)
			require.NoError(t, err)
			require.Len(t, *calls, 1)
			require.True(t, (*calls)[0].requester, "a requester: only what the new-ticket form shows")
			require.Equal(t, orig.ID, (*calls)[0].from.ID)
		})
	}
}

// Nothing is copied for a follow-up that was refused: there is no new ticket.
func TestFollowUp_NoCopyWhenTheFollowUpIsRefused(t *testing.T) {
	h := newHarness(t)
	calls := recordCopies(h)
	open := h.seedOpen()

	_, err := h.svc.CreateFollowUp(context.Background(), open.ID, "GHD", ticket.ChannelWeb,
		ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff})
	require.ErrorIs(t, err, ticket.ErrNotClosed)
	_, err = h.svc.CreateFollowUp(context.Background(), open.ID, "GHD", ticket.ChannelWeb, ticket.Actor{Role: user.RoleUser})
	require.ErrorIs(t, err, ticket.ErrForbidden)
	require.Empty(t, *calls)
}

// No copier wired (a unit test, a server built without one) leaves the
// follow-up as it was.
func TestFollowUp_WorksWithNoCopierWired(t *testing.T) {
	h := newHarness(t)
	orig := closedWithEverything(t, h)
	_, err := h.svc.CreateFollowUp(context.Background(), orig.ID, "GHD", ticket.ChannelWeb,
		ticket.Actor{UserID: ptr(uuid.New()), Role: user.RoleStaff})
	require.NoError(t, err)
}
