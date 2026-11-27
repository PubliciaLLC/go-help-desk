package ticket_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/catalogue"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

var openWithItem = func(context.Context, uuid.UUID, *uuid.UUID, *uuid.UUID) error { return nil }

// #711: what a requester's follow-up carries below the category is the caller's
// choice, made from the levels that requester sees.
func TestCreateRequesterFollowUpKeeping_CarriesTheLevelsItIsGiven(t *testing.T) {
	for name, tc := range map[string]struct {
		keep               catalogue.Levels
		wantType, wantItem bool
	}{
		"nothing below the category": {catalogue.Levels{}, false, false},
		"the type":                   {catalogue.Levels{Types: true}, true, false},
		"the type and the item":      {catalogue.Levels{Types: true, Items: true}, true, true},
		"an item needs its type":     {catalogue.Levels{Items: true}, false, false},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			orig := closedWithEverything(t, h) // type AND item
			require.NotNil(t, orig.ItemID)

			got, err := h.svc.CreateRequesterFollowUpKeeping(context.Background(), orig.ID, "GHD", ticket.ChannelWeb,
				reporterActor(orig), tc.keep, openWithItem)
			require.NoError(t, err)
			require.Equal(t, tc.wantType, got.TypeID != nil)
			require.Equal(t, tc.wantItem, got.ItemID != nil)
			if tc.wantType {
				require.Equal(t, orig.TypeID, got.TypeID)
			}
			if tc.wantItem {
				require.Equal(t, orig.ItemID, got.ItemID)
			}
		})
	}
}

// open sees the classification as it will be filed, the item included.
func TestCreateRequesterFollowUpKeeping_OpenSeesTheItem(t *testing.T) {
	h := newHarness(t)
	orig := closedWithEverything(t, h)
	var sawType, sawItem *uuid.UUID
	_, err := h.svc.CreateRequesterFollowUpKeeping(context.Background(), orig.ID, "GHD", ticket.ChannelWeb, reporterActor(orig),
		catalogue.Levels{Types: true, Items: true},
		func(_ context.Context, _ uuid.UUID, ty, it *uuid.UUID) error { sawType, sawItem = ty, it; return nil })
	require.NoError(t, err)
	require.Equal(t, orig.TypeID, sawType)
	require.Equal(t, orig.ItemID, sawItem)
}
