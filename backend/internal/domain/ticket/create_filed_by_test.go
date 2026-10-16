package ticket_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// #444: a member of staff files a ticket on behalf of someone else. The
// requester is the ticket's reporter (an account) or its guest (an address);
// the person who did the filing is the ACTOR, and the status history, the
// "created" audit entry and the "created" event say so. Without FiledBy the
// requester is the actor, as it always was.
func TestCreate_FiledBy(t *testing.T) {
	staffID, reporterID := uuid.New(), uuid.New()
	guest := "guest@example.test"

	cases := []struct {
		name      string
		in        ticket.CreateInput
		wantActor *uuid.UUID
	}{
		{
			name:      "an account requester, filed by staff",
			in:        ticket.CreateInput{ReporterUserID: &reporterID, FiledBy: &staffID},
			wantActor: &staffID,
		},
		{
			name:      "a guest requester, filed by staff",
			in:        ticket.CreateInput{GuestEmail: &guest, FiledBy: &staffID},
			wantActor: &staffID,
		},
		{
			name:      "no FiledBy: the account requester is the actor",
			in:        ticket.CreateInput{ReporterUserID: &reporterID},
			wantActor: &reporterID,
		},
		{
			name:      "no FiledBy: a guest has no actor",
			in:        ticket.CreateInput{GuestEmail: &guest},
			wantActor: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tc.in.Subject, tc.in.CategoryID = "Printer broken", uuid.New()

			got, err := h.svc.Create(context.Background(), tc.in)
			require.NoError(t, err)

			// The requester is still who the ticket is about: the row says so.
			require.Equal(t, tc.in.ReporterUserID, got.ReporterUserID)
			require.Equal(t, tc.in.GuestEmail, got.GuestEmail)

			hist := historyFor(h, got.ID)
			require.Len(t, hist, 1)
			require.Equal(t, tc.wantActor, hist[0].ChangedByUserID, "status history actor")

			var created int
			for _, e := range h.auditStore.entries {
				if e.EntityID == got.ID && e.Action == "created" {
					created++
					require.Equal(t, tc.wantActor, e.ActorID, "audit actor")
				}
			}
			require.Equal(t, 1, created)

			require.Len(t, h.dispatcher.events, 1)
			ev := h.dispatcher.events[0]
			require.Equal(t, notification.EventTicketCreated, ev.Type)
			require.Equal(t, tc.wantActor, ev.ActorID, "event actor")
			if tc.in.GuestEmail != nil {
				require.Equal(t, guest, ev.Recipient, "the guest is the recipient, not the filer")
				require.NotEmpty(t, ev.GuestToken, "and is sent a link")
			}
		})
	}
}
