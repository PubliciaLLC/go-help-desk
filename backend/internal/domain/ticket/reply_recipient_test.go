package ticket_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

func TestReplyRecipient(t *testing.T) {
	guest := "guest@example.com"
	empty := ""
	acct := uuid.New()
	gone := uuid.New()
	other := uuid.New()
	lookup := func(_ context.Context, id uuid.UUID) (string, error) {
		if id == acct {
			return "acct@example.com", nil
		}
		return "", errors.New("not found")
	}
	as := func(id uuid.UUID, role user.Role) ticket.Actor { return ticket.Actor{UserID: &id, Role: role} }

	tests := []struct {
		name   string
		tk     ticket.Ticket
		author ticket.Actor
		lookup func(context.Context, uuid.UUID) (string, error)
		want   string
	}{
		{"guest address", ticket.Ticket{GuestEmail: &guest}, as(other, user.RoleStaff), lookup, guest},
		{"guest wins over a reporter", ticket.Ticket{GuestEmail: &guest, ReporterUserID: &acct}, as(other, user.RoleStaff), lookup, guest},
		{"account address", ticket.Ticket{ReporterUserID: &acct}, as(other, user.RoleStaff), lookup, "acct@example.com"},
		{"account gone", ticket.Ticket{ReporterUserID: &gone}, as(other, user.RoleStaff), lookup, ""},
		{"no lookup wired", ticket.Ticket{ReporterUserID: &acct}, as(other, user.RoleStaff), nil, ""},
		{"nobody on record", ticket.Ticket{}, as(other, user.RoleStaff), lookup, ""},
		// A present but empty guest address is returned as is; the dispatcher skips it.
		{"empty guest address", ticket.Ticket{GuestEmail: &empty}, as(other, user.RoleStaff), lookup, ""},
		// Erik's decision: staff and admins are not mailed a copy of their own reply...
		{"staff author is the requester", ticket.Ticket{ReporterUserID: &acct}, as(acct, user.RoleStaff), lookup, ""},
		{"admin author is the requester", ticket.Ticket{ReporterUserID: &acct}, as(acct, user.RoleAdmin), lookup, ""},
		// ...but an ordinary requester still is, as before.
		{"requester replying on their own ticket", ticket.Ticket{ReporterUserID: &acct}, as(acct, user.RoleUser), lookup, "acct@example.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.svc.SetUserEmailLookup(tc.lookup)
			require.Equal(t, tc.want, h.svc.ReplyRecipient(context.Background(), tc.tk, tc.author))
		})
	}
}
