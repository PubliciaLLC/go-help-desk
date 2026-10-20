package ticket_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// A reply is held to the same length as a description: 50,000, counted the way
// the description is counted (bytes of the stored text). A megabyte reply could
// be stored and was then a burden on every page that showed it.

func TestMaxReplyLength_IsTheDescriptionsLimit(t *testing.T) {
	require.Equal(t, ticket.MaxDescriptionLength, ticket.MaxReplyLength)
	require.Equal(t, 50_000, ticket.MaxReplyLength)
}

func TestAddReply_LengthLimit(t *testing.T) {
	staff := func() ticket.Actor { id := uuid.New(); return ticket.Actor{UserID: &id, Role: user.RoleStaff} }
	for _, tc := range []struct {
		name string
		body string
		ok   bool
	}{
		{"exactly the limit", strings.Repeat("a", ticket.MaxReplyLength), true},
		{"one over", strings.Repeat("a", ticket.MaxReplyLength+1), false},
		{"a megabyte", strings.Repeat("a", 1_000_000), false},
		// Counted in bytes, as a description is: 25,000 two-byte characters fit and 25,001 do not.
		{"multi-byte, at the limit", strings.Repeat("é", ticket.MaxReplyLength/2), true},
		{"multi-byte, one character over", strings.Repeat("é", ticket.MaxReplyLength/2+1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			seeded := h.seedOpen()
			_, err := h.svc.AddReply(context.Background(), seeded.ID, tc.body, ticket.TextFormatPlain, false, false, "", staff(), 7, h.newStatus.ID)
			replies, lerr := h.svc.ListReplies(context.Background(), seeded.ID)
			require.NoError(t, lerr)
			if tc.ok {
				require.NoError(t, err)
				require.Len(t, replies, 1)
				return
			}
			require.ErrorIs(t, err, ticket.ErrReplyTooLong)
			require.ErrorIs(t, err, ticket.ErrValidation, "still a validation refusal, so every caller already maps it")
			require.Empty(t, replies, "nothing is written")
			require.Empty(t, h.dispatcher.events, "and nothing is announced")
		})
	}
}

func TestAddGuestReply_LengthLimit(t *testing.T) {
	h := newHarness(t)
	seeded := h.seedOpen()
	_, err := h.svc.AddGuestReply(context.Background(), seeded.ID, strings.Repeat("a", ticket.MaxReplyLength+1), ticket.TextFormatPlain, 7, h.newStatus.ID)
	require.ErrorIs(t, err, ticket.ErrReplyTooLong)
	_, err = h.svc.AddGuestReply(context.Background(), seeded.ID, strings.Repeat("a", ticket.MaxReplyLength), ticket.TextFormatPlain, 7, h.newStatus.ID)
	require.NoError(t, err)
}
