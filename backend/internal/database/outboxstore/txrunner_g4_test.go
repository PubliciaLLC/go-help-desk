package outboxstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/txrunner"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// The new events (#630) are written with the change like any other: a webhook
// row, and an email row only when the event can mean mail.
func TestInTx_G4EventsQueueTheRowsTheirChannelsCarry(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	r := txrunner.New(db.SQL, []string{"email", "webhook"}, nil)
	holder := uuid.New()

	for _, tc := range []struct {
		name string
		ev   notification.Event
		want map[string]int
	}{
		{"an ordinary assignment may mean staff mail",
			notification.Event{Kinds: []notification.EventType{notification.EventTicketAssigned}},
			map[string]int{"email": 1, "webhook": 1}},
		{"a bulk unassignment is announced and mails nobody",
			notification.Event{Kinds: []notification.EventType{notification.EventTicketAssigned}, SuppressStaffMail: true},
			map[string]int{"webhook": 1}},
		{"an unlink",
			notification.Event{Kinds: []notification.EventType{notification.EventTicketUnlinked},
				Payload: map[string]any{"target_id": uuid.New(), "link_type": "related_to"}},
			map[string]int{"webhook": 1}},
		{"an attachment",
			notification.Event{Kinds: []notification.EventType{notification.EventTicketAttachmentAdded},
				Payload: map[string]any{"attachment_id": uuid.New()}},
			map[string]int{"webhook": 1}},
		{"a follow",
			notification.Event{Kinds: []notification.EventType{notification.EventTicketFollowed},
				Payload: map[string]any{"user_id": uuid.New()}},
			map[string]int{"webhook": 1}},
		{"an unfollow",
			notification.Event{Kinds: []notification.EventType{notification.EventTicketUnfollowed},
				Payload: map[string]any{"user_id": uuid.New()}},
			map[string]int{"webhook": 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tid := uuid.New()
			ev := tc.ev
			ev.TicketID, ev.OccurredAt = tid, time.Now()
			ev.Ticket = &notification.TicketState{Priority: "low", AssigneeUserID: &holder}
			require.NoError(t, r.InTx(context.Background(), func(_ ticket.Store, _ audit.Store, ch *notification.Changes) error {
				ch.Raise(ev)
				return nil
			}))
			require.Equal(t, tc.want, outboxRows(t, db, tid))
		})
	}
}
