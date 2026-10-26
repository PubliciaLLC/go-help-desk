package ticket_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// #444: an existing user a member of staff files a ticket for is told, with the
// ordinary "We have received your ticket" mail: the created event carries their
// address as its Recipient, with no guest link (they sign in). Nobody else is
// newly mailed: a member of staff filing their own ticket, or a requester
// filing theirs, keeps today's behaviour, which is no mail to the filer.
func TestCreate_NotifiesAnAccountFiledFor(t *testing.T) {
	staffID, reporterID, noMailID := uuid.New(), uuid.New(), uuid.New()
	lookup := func(_ context.Context, id uuid.UUID) (string, error) {
		switch id {
		case reporterID:
			return "reporter@example.test", nil
		case staffID:
			return "staff@example.test", nil
		case noMailID:
			return "", nil // a service account: no address
		}
		return "", errors.New("not found")
	}

	cases := []struct {
		name          string
		reporter      uuid.UUID
		filedBy       *uuid.UUID
		wantRecipient string
	}{
		{"filed for another account", reporterID, &staffID, "reporter@example.test"},
		{"the requester filed it themselves", reporterID, nil, ""},
		{"staff filed it for themselves", staffID, &staffID, ""},
		{"filed for a service account, which has no address", noMailID, &staffID, ""},
		{"filed for an account the lookup cannot read", uuid.New(), &staffID, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.svc.SetUserEmailLookup(lookup)
			rid := tc.reporter

			_, err := h.svc.Create(context.Background(), ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
				Subject: "Printer broken", CategoryID: uuid.New(), ReporterUserID: &rid, FiledBy: tc.filedBy,
			})
			require.NoError(t, err)

			require.Len(t, h.dispatcher.events, 1)
			ev := h.dispatcher.events[0]
			require.Equal(t, notification.EventTicketCreated, ev.Type)
			require.Equal(t, tc.wantRecipient, ev.Recipient)
			require.False(t, ev.GuestLink, "an account holder is sent no guest link")
			require.Empty(t, ev.GuestToken)
		})
	}

	t.Run("no lookup wired", func(t *testing.T) {
		h := newHarness(t)
		rid := reporterID
		_, err := h.svc.Create(context.Background(), ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
			Subject: "Printer broken", CategoryID: uuid.New(), ReporterUserID: &rid, FiledBy: &staffID,
		})
		require.NoError(t, err)
		require.Equal(t, "", h.dispatcher.events[0].Recipient, "the ticket is filed; only the mail is lost")
	})
}
