package ticket_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// #130: the domain never fills in a channel. The entry point names it, so a
// caller that forgets must fail here rather than be recorded as something.

func TestSourceChannel_Valid(t *testing.T) {
	for _, tc := range []struct {
		c  ticket.SourceChannel
		ok bool
	}{
		{ticket.ChannelWeb, true}, {ticket.ChannelAPI, true}, {ticket.ChannelMCP, true},
		{ticket.ChannelGuest, true}, {ticket.ChannelPhone, true},
		// email is reserved: it is not valid until a migration adds it.
		{"email", false}, {"", false}, {"Web", false}, {" web", false}, {"sms", false},
	} {
		require.Equal(t, tc.ok, tc.c.Valid(), "%q", tc.c)
	}
}

func TestCreate_RefusesAnEmptyOrUnknownSourceChannel(t *testing.T) {
	for _, c := range []ticket.SourceChannel{"", "email", "Web", "carrier-pigeon"} {
		h := newHarness(t)
		reporter := uuid.New()
		in := ticket.CreateInput{
			Subject: "s", Description: "d", DescriptionFormat: ticket.TextFormatPlain, SourceChannel: c,
			CategoryID: uuid.New(), ReporterUserID: &reporter,
		}
		_, err := h.svc.Create(context.Background(), in)
		require.ErrorIs(t, err, ticket.ErrInvalidSourceChannel, "channel %q", c)
		require.ErrorIs(t, err, ticket.ErrValidation, "a 400, not a 500")
		require.ErrorIs(t, h.svc.ValidateCreate(context.Background(), in), ticket.ErrInvalidSourceChannel,
			"ValidateCreate refuses what Create refuses")
		require.Empty(t, h.dispatcher.events, "nothing is announced for a ticket that was not written")
	}
}

func TestCreate_StoresTheChannelItWasGiven(t *testing.T) {
	filer := uuid.New()
	for _, c := range []ticket.SourceChannel{
		ticket.ChannelWeb, ticket.ChannelAPI, ticket.ChannelMCP, ticket.ChannelGuest, ticket.ChannelPhone,
	} {
		h := newHarness(t)
		reporter := uuid.New()
		got, err := h.svc.Create(context.Background(), ticket.CreateInput{
			Subject: "s", Description: "d", DescriptionFormat: ticket.TextFormatPlain, SourceChannel: c,
			CategoryID: uuid.New(), ReporterUserID: &reporter, FiledBy: &filer,
		})
		require.NoError(t, err, "channel %q", c)
		require.Equal(t, c, got.SourceChannel)
	}
}

// "phone" means somebody took the call and filed for the caller. A ticket with
// nobody who filed it cannot have come that way.
func TestCreate_PhoneRequiresSomeoneFilingForThem(t *testing.T) {
	h := newHarness(t)
	reporter := uuid.New()
	in := ticket.CreateInput{
		Subject: "s", Description: "d", DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelPhone,
		CategoryID: uuid.New(), ReporterUserID: &reporter,
	}
	_, err := h.svc.Create(context.Background(), in)
	require.ErrorIs(t, err, ticket.ErrValidation)
	require.ErrorContains(t, err, "phone is for staff filing on someone's behalf")
	// ValidateCreate runs before the caller has said who filed, so it cannot ask.
	require.NoError(t, h.svc.ValidateCreate(context.Background(), in))
}
