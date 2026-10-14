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
	lookup := func(_ context.Context, id uuid.UUID) (string, error) {
		if id == acct {
			return "acct@example.com", nil
		}
		return "", errors.New("not found")
	}

	tests := []struct {
		name   string
		tk     ticket.Ticket
		lookup func(context.Context, uuid.UUID) (string, error)
		want   string
	}{
		{"guest address", ticket.Ticket{GuestEmail: &guest}, lookup, guest},
		{"guest wins over a reporter", ticket.Ticket{GuestEmail: &guest, ReporterUserID: &acct}, lookup, guest},
		{"account address", ticket.Ticket{ReporterUserID: &acct}, lookup, "acct@example.com"},
		{"account gone", ticket.Ticket{ReporterUserID: &gone}, lookup, ""},
		{"no lookup wired", ticket.Ticket{ReporterUserID: &acct}, nil, ""},
		{"nobody on record", ticket.Ticket{}, lookup, ""},
		// Characterises the REST rule this was lifted from: a present but empty
		// guest address is returned as is, and the dispatcher skips it.
		{"empty guest address", ticket.Ticket{GuestEmail: &empty}, lookup, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.svc.SetUserEmailLookup(tc.lookup)
			require.Equal(t, tc.want, h.svc.ReplyRecipient(context.Background(), tc.tk))
		})
	}
}
