package ticket_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

func TestReplyRecipient(t *testing.T) {
	guest := "guest@example.com"
	empty := ""
	acct := uuid.New()
	gone := uuid.New()
	staff := uuid.New()
	lookup := func(_ context.Context, id uuid.UUID) (string, error) {
		if id == acct || id == staff {
			return "acct@example.com", nil
		}
		return "", errors.New("not found")
	}

	tests := []struct {
		name   string
		tk     ticket.Ticket
		author uuid.UUID
		lookup func(context.Context, uuid.UUID) (string, error)
		want   string
	}{
		{"guest address", ticket.Ticket{GuestEmail: &guest}, staff, lookup, guest},
		{"guest wins over a reporter", ticket.Ticket{GuestEmail: &guest, ReporterUserID: &acct}, staff, lookup, guest},
		{"account address", ticket.Ticket{ReporterUserID: &acct}, staff, lookup, "acct@example.com"},
		{"account gone", ticket.Ticket{ReporterUserID: &gone}, staff, lookup, ""},
		{"no lookup wired", ticket.Ticket{ReporterUserID: &acct}, staff, nil, ""},
		{"nobody on record", ticket.Ticket{}, staff, lookup, ""},
		// A present but empty guest address is returned as is; the dispatcher skips it.
		{"empty guest address", ticket.Ticket{GuestEmail: &empty}, staff, lookup, ""},
		// Erik's decision: nobody is mailed about their own reply, even as the requester.
		{"the author is the requester", ticket.Ticket{ReporterUserID: &staff}, staff, lookup, ""},
		{"a different author does not exclude", ticket.Ticket{ReporterUserID: &staff}, acct, lookup, "acct@example.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.svc.SetUserEmailLookup(tc.lookup)
			require.Equal(t, tc.want, h.svc.ReplyRecipient(context.Background(), tc.tk, tc.author))
		})
	}
}
