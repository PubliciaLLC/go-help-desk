package server_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Postgres refuses NUL in text, which used to be a 500 and, on a create, a
// tracking number already taken (a gap in the sequence). It is a 400 now, for
// every free-text field. Nothing else is refused or changed: 1.2 stored tabs,
// newlines, ESC and the rest as given, and so does this.

func TestNUL_IsRefusedAs400OnCreate(t *testing.T) {
	for _, field := range []string{"subject", "description"} {
		t.Run(field, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()

			res := h.postOnBehalf(t, "staff", map[string]any{field: "a\x00b"})
			body := readBody(t, res)
			require.Equal(t, http.StatusBadRequest, res.StatusCode, body)
			require.Contains(t, body, field)
			require.Equal(t, 0, ticketCount(t, h))
		})
	}
}

func TestNUL_IsRefusedAs400OnReply(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := decodeTicket(t, h.postOnBehalf(t, "staff", nil))

	res := h.do(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/replies", map[string]any{"body": "a\x00b"})
	require.Equal(t, http.StatusBadRequest, res.StatusCode, readBody(t, res))
}

// Everything else is stored as given, in a subject, a description and a reply.
func TestOtherControlCharactersAreStoredAsGiven(t *testing.T) {
	for _, v := range []string{"a\tb", "a\nb", "a\x1b[31mb", "a\x07b", "a\fb", "a\u0085b"} {
		t.Run(fmt.Sprintf("%q", v), func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()

			tk := decodeTicket(t, h.postOnBehalf(t, "staff", map[string]any{"subject": v, "description": v}))
			stored, err := h.ticketSvc.GetByID(context.Background(), tk.ID)
			require.NoError(t, err)
			require.Equal(t, v, stored.Subject)
			require.Equal(t, v, stored.Description)

			res := h.do(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/replies", map[string]any{"body": v})
			require.Equal(t, http.StatusCreated, res.StatusCode, readBody(t, res))
		})
	}
}

// A follow-up copies the closed ticket's stored subject and description into a
// create. Whatever 1.2 stored must still be followable.
func TestFollowUp_OfATicketWithTabsAndEscapeCodesStillWorks(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	subject, description := "Printer\tjam\nagain", "\x1b[31mred\x1b[0m\r\nbell\x07"
	ctx := context.Background()
	orig, err := h.ticketSvc.Create(ctx, ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		Subject: subject, Description: description, CategoryID: h.catID,
		Priority: ticket.PriorityLow, ReporterUserID: &h.userID,
	})
	require.NoError(t, err, "stored as given, as 1.2 did")
	require.NoError(t, h.ticketSvc.Close(ctx, orig.ID, ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin}))

	got := decodeTicket(t, h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+orig.ID.String()+"/follow-up", nil))
	require.Equal(t, subject, got.Subject)
	require.Equal(t, description, got.Description)
}
