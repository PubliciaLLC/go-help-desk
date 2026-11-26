package ticket_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/catalogue"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// #711: a requester's view of a ticket carries a level of the classification
// only if the one policy lets them see that level.

var allLevels = catalogue.Levels{Types: true, Items: true}

// item_id and type_id are on the allowed list, which is the most a requester
// can ever be sent; the conditional lists name what is withheld below it, so the
// classification test above still decides every key and these are a subset of it.
func TestRequesterLevelKeys_AreOnTheAllowedList(t *testing.T) {
	allowed := asSet(ticket.RequesterTicketKeys())
	require.Equal(t, []string{"item_id"}, ticket.RequesterItemKeys())
	require.Equal(t, []string{"type_id"}, ticket.RequesterTypeKeys())
	for _, k := range append(ticket.RequesterItemKeys(), ticket.RequesterTypeKeys()...) {
		require.True(t, allowed[k], "%q is conditional, so it must be on the allowed list it is subtracted from", k)
	}
}

func TestFilterForViewer_WithoutItemsTheRequesterGetsNoItem(t *testing.T) {
	tk := filledTicket(t)
	require.NotNil(t, tk.ItemID, "the fixture has an item, or this proves nothing")
	noItems := catalogue.Levels{Types: true}

	for _, role := range []user.Role{user.RoleUser, user.Role(""), user.Role("auditor")} {
		t.Run("one ticket/"+string(role), func(t *testing.T) {
			got, err := ticket.FilterForViewer(role, noItems, tk)
			require.NoError(t, err)
			keys := keysOf(t, got)
			require.NotContains(t, keys, "item_id")
			require.Contains(t, keys, "type_id")
			require.Contains(t, keys, "category_id")
			require.Len(t, keys, len(ticket.RequesterTicketKeys())-1, "everything else is as it was")
		})
		t.Run("list/"+string(role), func(t *testing.T) {
			got, err := ticket.FilterForViewer(role, noItems, []ticket.Ticket{tk, tk})
			require.NoError(t, err)
			b, _ := json.Marshal(got)
			var rows []map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(b, &rows))
			require.Len(t, rows, 2)
			for _, row := range rows {
				require.NotContains(t, row, "item_id")
				require.Contains(t, row, "type_id")
			}
		})
		t.Run("wrapped/"+string(role), func(t *testing.T) {
			wrapped := struct {
				ticket.Ticket
				CanReopen bool `json:"can_reopen"`
			}{Ticket: tk}
			got, err := ticket.FilterForViewer(role, noItems, wrapped)
			require.NoError(t, err)
			require.NotContains(t, keysOf(t, got), "item_id")
		})
	}
}

func TestFilterForViewer_WithoutTypesTheRequesterGetsNeitherTypeNorItem(t *testing.T) {
	got, err := ticket.FilterForViewer(user.RoleUser, catalogue.Levels{}, filledTicket(t))
	require.NoError(t, err)
	keys := keysOf(t, got)
	require.NotContains(t, keys, "type_id")
	require.NotContains(t, keys, "item_id", "an item hangs under a type")
	require.Contains(t, keys, "category_id")
}

func TestFilterForViewer_WithItemsTheRequesterKeepsIt(t *testing.T) {
	got, err := ticket.FilterForViewer(user.RoleUser, allLevels, filledTicket(t))
	require.NoError(t, err)
	require.Contains(t, keysOf(t, got), "item_id")
}

// Staff and administrators are untouched whatever the levels say: the levels
// are what a caller sees, and staff see everything.
func TestFilterForViewer_StaffKeepTheItemWhateverTheLevelsSay(t *testing.T) {
	tk := filledTicket(t)
	for _, role := range []user.Role{user.RoleStaff, user.RoleAdmin} {
		got, err := ticket.FilterForViewer(role, catalogue.Levels{}, tk)
		require.NoError(t, err)
		require.Equal(t, tk, got)
	}
}
