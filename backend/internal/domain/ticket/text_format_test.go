package ticket_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// #443: the domain never fills in a format. The right default differs by
// caller (REST plain, MCP markdown), so a caller that forgets must fail here.

func TestParseTextFormat(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want ticket.TextFormat
		ok   bool
	}{
		{"plain", ticket.TextFormatPlain, true},
		{"markdown", ticket.TextFormatMarkdown, true},
		{"", "", false},
		{"Markdown", "", false},
		{"PLAIN", "", false},
		{"html", "", false},
		{" plain", "", false},
	} {
		got, err := ticket.ParseTextFormat(tc.in)
		if tc.ok {
			require.NoError(t, err, tc.in)
			require.Equal(t, tc.want, got)
		} else {
			require.ErrorIs(t, err, ticket.ErrInvalidTextFormat, tc.in)
		}
	}
}

func TestCreate_RefusesAnEmptyOrUnknownDescriptionFormat(t *testing.T) {
	for _, f := range []ticket.TextFormat{"", "html", "Markdown"} {
		h := newHarness(t)
		reporter := uuid.New()
		_, err := h.svc.Create(context.Background(), ticket.CreateInput{
			Subject: "s", Description: "d", DescriptionFormat: f,
			CategoryID: uuid.New(), ReporterUserID: &reporter,
		})
		require.ErrorIs(t, err, ticket.ErrInvalidTextFormat, "format %q", f)
		require.Empty(t, h.dispatcher.events, "nothing is announced for a ticket that was not written")
	}
}

func TestCreate_StoresTheFormatItWasGiven(t *testing.T) {
	for _, f := range []ticket.TextFormat{ticket.TextFormatPlain, ticket.TextFormatMarkdown} {
		h := newHarness(t)
		reporter := uuid.New()
		got, err := h.svc.Create(context.Background(), ticket.CreateInput{
			Subject: "s", Description: "d", DescriptionFormat: f,
			CategoryID: uuid.New(), ReporterUserID: &reporter,
		})
		require.NoError(t, err)
		require.Equal(t, f, got.DescriptionFormat)
	}
}

func TestAddReply_RefusesAnEmptyOrUnknownFormat(t *testing.T) {
	for _, f := range []ticket.TextFormat{"", "html"} {
		h := newHarness(t)
		reporter := uuid.New()
		seeded := h.seedResolved(reporter)
		staff := ticket.Actor{UserID: &reporter, Role: user.RoleStaff}

		_, err := h.svc.AddReply(context.Background(), seeded.ID, "x", f, false, false, "", staff, 7, h.newStatus.ID)
		require.ErrorIs(t, err, ticket.ErrInvalidTextFormat, "AddReply %q", f)
		_, err = h.svc.AddGuestReply(context.Background(), seeded.ID, "x", f, 7, h.newStatus.ID)
		require.ErrorIs(t, err, ticket.ErrInvalidTextFormat, "AddGuestReply %q", f)
	}
}

func TestAddReply_StoresTheFormatItWasGiven(t *testing.T) {
	for _, f := range []ticket.TextFormat{ticket.TextFormatPlain, ticket.TextFormatMarkdown} {
		h := newHarness(t)
		reporter := uuid.New()
		seeded := h.seedResolved(reporter)
		got, err := h.svc.AddReply(context.Background(), seeded.ID, "x", f, false, false, "",
			ticket.Actor{UserID: &reporter, Role: user.RoleUser}, 30, h.newStatus.ID)
		require.NoError(t, err)
		require.Equal(t, f, got.BodyFormat)
	}
}

// ValidateCreate is create's checks without the write; it must refuse the same
// format create refuses, or a caller that validates first and writes later
// would see two different rules.
func TestValidateCreate_RefusesAnEmptyOrUnknownDescriptionFormat(t *testing.T) {
	for _, f := range []ticket.TextFormat{"", "html"} {
		h := newHarness(t)
		reporter := uuid.New()
		err := h.svc.ValidateCreate(context.Background(), ticket.CreateInput{
			Subject: "s", Description: "d", DescriptionFormat: f,
			CategoryID: uuid.New(), ReporterUserID: &reporter,
		})
		require.ErrorIs(t, err, ticket.ErrInvalidTextFormat, "format %q", f)
	}
}
