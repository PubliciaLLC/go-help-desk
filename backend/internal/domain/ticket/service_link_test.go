package ticket_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

func TestAddLink_InvalidLinkType(t *testing.T) {
	h := newHarness(t)
	source := h.seedOpen()
	target := h.seedOpen()
	agent := uuid.New()

	// Try to create a link with an invalid link type
	err := h.svc.AddLink(context.Background(), source.ID, target.ID, ticket.LinkType("invalid_type"),
		ticket.Actor{UserID: &agent, Role: user.RoleStaff})

	require.Error(t, err)
	require.ErrorIs(t, err, ticket.ErrInvalidLinkType)

	// Verify no link was created
	links, _ := h.store.ListLinks(context.Background(), source.ID)
	require.Empty(t, links, "no link should be created for an invalid link type")
}

func TestAddLink_DuplicateLink(t *testing.T) {
	h := newHarness(t)
	source := h.seedOpen()
	target := h.seedOpen()
	agent := uuid.New()

	// Create the link the first time
	err := h.svc.AddLink(context.Background(), source.ID, target.ID, ticket.LinkRelatedTo,
		ticket.Actor{UserID: &agent, Role: user.RoleStaff})
	require.NoError(t, err)

	// Try to create the same link again
	err = h.svc.AddLink(context.Background(), source.ID, target.ID, ticket.LinkRelatedTo,
		ticket.Actor{UserID: &agent, Role: user.RoleStaff})

	require.Error(t, err)
	require.ErrorIs(t, err, ticket.ErrLinkAlreadyExists)

	// Verify only one link exists
	links, _ := h.store.ListLinks(context.Background(), source.ID)
	require.Len(t, links, 1, "only the first link should exist")
	require.Equal(t, source.ID, links[0].SourceTicketID)
	require.Equal(t, target.ID, links[0].TargetTicketID)
	require.Equal(t, ticket.LinkRelatedTo, links[0].LinkType)
}

func TestAddLink_ValidTypes(t *testing.T) {
	h := newHarness(t)
	agent := uuid.New()

	tests := []struct {
		name     string
		linkType ticket.LinkType
	}{
		{"related_to", ticket.LinkRelatedTo},
		{"parent_child", ticket.LinkParentChild},
		{"caused_by", ticket.LinkCausedBy},
		{"duplicate_of", ticket.LinkDuplicateOf},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := h.seedOpen()
			target := h.seedOpen()

			err := h.svc.AddLink(context.Background(), source.ID, target.ID, tt.linkType,
				ticket.Actor{UserID: &agent, Role: user.RoleStaff})
			require.NoError(t, err)

			links, _ := h.store.ListLinks(context.Background(), source.ID)
			require.Len(t, links, 1)
			require.Equal(t, tt.linkType, links[0].LinkType)
		})
	}
}
