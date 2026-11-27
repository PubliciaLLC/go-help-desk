package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// #711, routing and follow-ups.
//
// Routing is by category and type (items do not factor into scope). Two groups:
// one scoped to the category and the Laptop type, one to the category alone.
// Groups are tried in name order and a category-wide scope matches every type,
// so the type group is named to sort first: a ticket that carries the type goes
// to it, and one without goes to the category group because the type group does
// not cover a ticket with no type.
type routedTree struct {
	choiceTree
	typeGroup, catGroup uuid.UUID
}

func buildRoutedTree(t *testing.T, h *harness) routedTree {
	t.Helper()
	ctx := context.Background()
	tree := buildChoiceTree(t, h)
	tg, err := h.groupSvc.Create(ctx, "A Hardware Laptop", "")
	require.NoError(t, err)
	require.NoError(t, h.groupSvc.AddScope(ctx, group.GroupScope{GroupID: tg.ID, CategoryID: tree.cat, TypeID: idp(tree.typ)}))
	cg, err := h.groupSvc.Create(ctx, "B Hardware", "")
	require.NoError(t, err)
	require.NoError(t, h.groupSvc.AddScope(ctx, group.GroupScope{GroupID: cg.ID, CategoryID: tree.cat}))
	return routedTree{choiceTree: tree, typeGroup: tg.ID, catGroup: cg.ID}
}

func groupOf(t *testing.T, h *harness, id uuid.UUID) *uuid.UUID {
	t.Helper()
	tk, err := h.ticketSvc.GetByID(context.Background(), id)
	require.NoError(t, err)
	return tk.AssigneeGroupID
}

func guestCreatedID(t *testing.T, h *harness, body []byte) uuid.UUID {
	t.Helper()
	tk, err := h.ticketSvc.GetByTrackingNumber(context.Background(), tracking(trackingOf(t, body)))
	require.NoError(t, err)
	return tk.ID
}

// A guest's chosen Type routes the ticket: it lands in the group scoped to
// category and type, and one filed without a type lands in the category group.
func TestCTIChoice_GuestTypeChangesRouting(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tree := buildRoutedTree(t, h)
	setChoice(t, h, admin.KeyGuestSubmissionEnabled, true)
	setChoice(t, h, guestsTypeItemKey, true)

	code, body := guestPost(t, h, map[string]any{"category_id": tree.cat.String(), "type_id": tree.typ.String()})
	require.Equal(t, http.StatusCreated, code, string(body))
	require.Equal(t, idp(tree.typeGroup), groupOf(t, h, guestCreatedID(t, h, body)), "the type's group")

	code, body = guestPost(t, h, map[string]any{"category_id": tree.cat.String()})
	require.Equal(t, http.StatusCreated, code, string(body))
	require.Equal(t, idp(tree.catGroup), groupOf(t, h, guestCreatedID(t, h, body)), "no type: the category's group")

	// Off, a type sent is ignored and so is not routed on.
	setChoice(t, h, guestsTypeItemKey, false)
	code, body = guestPost(t, h, map[string]any{"category_id": tree.cat.String(), "type_id": tree.typ.String()})
	require.Equal(t, http.StatusCreated, code, string(body))
	require.Equal(t, idp(tree.catGroup), groupOf(t, h, guestCreatedID(t, h, body)))
}

// ── follow-ups ───────────────────────────────────────────────────────────────

// closedFor files a ticket under the tree's type and item for the reporting
// user, or as a guest, and closes it. A guest's is returned with its token.
func closedFor(t *testing.T, h *harness, tree choiceTree, asGuest bool) (ticket.Ticket, string) {
	t.Helper()
	ctx := context.Background()
	in := ticket.CreateInput{
		DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		Subject: "Dock dead", Description: "x", CategoryID: tree.cat, TypeID: idp(tree.typ), ItemID: idp(tree.item),
	}
	if asGuest {
		email := "guest@test.local"
		in.GuestEmail, in.GuestName = &email, "Ada"
	} else {
		in.ReporterUserID = &h.userID
	}
	tk, err := h.ticketSvc.Create(ctx, in)
	require.NoError(t, err)
	require.NoError(t, h.ticketSvc.Close(ctx, tk.ID, ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin}))
	if !asGuest {
		return tk, ""
	}
	token, err := h.ticketSvc.IssueGuestToken(ctx, tk.ID)
	require.NoError(t, err)
	return tk, token
}

func requesterFollowUp(t *testing.T, h *harness, orig ticket.Ticket) (int, ticket.Ticket) {
	t.Helper()
	res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets/"+orig.ID.String()+"/follow-up", nil)
	body := readBody(t, res)
	if res.StatusCode != http.StatusCreated {
		return res.StatusCode, ticket.Ticket{}
	}
	var got ticket.Ticket
	require.NoError(t, json.Unmarshal([]byte(body), &got))
	stored, err := h.ticketSvc.GetByID(context.Background(), got.ID)
	require.NoError(t, err)
	return res.StatusCode, stored
}

func guestFollowUp(t *testing.T, h *harness, token string) (int, ticket.Ticket) {
	t.Helper()
	res := h.doGuest(t, http.MethodPost, "/api/v1/guest/follow-up", token, nil)
	body := readBody(t, res)
	if res.StatusCode != http.StatusCreated {
		return res.StatusCode, ticket.Ticket{}
	}
	tk, err := h.ticketSvc.GetByTrackingNumber(context.Background(), tracking(trackingOf(t, []byte(body))))
	require.NoError(t, err)
	return res.StatusCode, tk
}

func TestCTIChoice_RequesterFollowUpKeepsTheChoice(t *testing.T) {
	t.Run("item switch off: the type is kept and the item is not, as before", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		tree := buildChoiceTree(t, h)
		code, got := requesterFollowUp(t, h, mustFirst(closedFor(t, h, tree, false)))
		require.Equal(t, http.StatusCreated, code)
		require.Equal(t, idp(tree.typ), got.TypeID)
		require.Nil(t, got.ItemID)
	})
	t.Run("item switch on: the item is kept too", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		tree := buildChoiceTree(t, h)
		setChoice(t, h, usersItemKey, true)
		code, got := requesterFollowUp(t, h, mustFirst(closedFor(t, h, tree, false)))
		require.Equal(t, http.StatusCreated, code)
		require.Equal(t, idp(tree.typ), got.TypeID)
		require.Equal(t, idp(tree.item), got.ItemID)
	})
	t.Run("item switch on but the item has been archived since: filed without it, silently", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		ctx := context.Background()
		tree := buildChoiceTree(t, h)
		setChoice(t, h, usersItemKey, true)
		orig := mustFirst(closedFor(t, h, tree, false))
		it, err := h.categorySvc.GetItem(ctx, tree.item)
		require.NoError(t, err)
		it.Active = false
		require.NoError(t, h.categorySvc.UpdateItem(ctx, it))
		code, got := requesterFollowUp(t, h, orig)
		require.Equal(t, http.StatusCreated, code, "the deepest level still active is kept, not refused")
		require.Equal(t, idp(tree.typ), got.TypeID)
		require.Nil(t, got.ItemID)
	})
	t.Run("the type has been archived since: the category alone, whatever the item switch says", func(t *testing.T) {
		for _, on := range []bool{false, true} {
			h, cleanup := newHarness(t)
			ctx := context.Background()
			tree := buildChoiceTree(t, h)
			setChoice(t, h, usersItemKey, on)
			orig := mustFirst(closedFor(t, h, tree, false))
			ty, err := h.categorySvc.GetType(ctx, tree.typ)
			require.NoError(t, err)
			ty.Active = false
			require.NoError(t, h.categorySvc.UpdateType(ctx, ty))
			code, got := requesterFollowUp(t, h, orig)
			require.Equal(t, http.StatusCreated, code, "items shown %v", on)
			require.Equal(t, orig.CategoryID, got.CategoryID)
			require.Nil(t, got.TypeID)
			require.Nil(t, got.ItemID)
			cleanup()
		}
	})
	t.Run("item switch off and the item archived: unaffected, as before", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		ctx := context.Background()
		tree := buildChoiceTree(t, h)
		orig := mustFirst(closedFor(t, h, tree, false))
		it, err := h.categorySvc.GetItem(ctx, tree.item)
		require.NoError(t, err)
		it.Active = false
		require.NoError(t, h.categorySvc.UpdateItem(ctx, it))
		code, got := requesterFollowUp(t, h, orig)
		require.Equal(t, http.StatusCreated, code)
		require.Nil(t, got.ItemID)
	})
}

func TestCTIChoice_GuestFollowUpKeepsWhatIsShownAndRoutesOnTheType(t *testing.T) {
	t.Run("switch off: no type and the category's group, as before", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		tree := buildRoutedTree(t, h)
		setChoice(t, h, admin.KeyGuestSubmissionEnabled, true)
		_, token := closedFor(t, h, tree.choiceTree, true)
		code, got := guestFollowUp(t, h, token)
		require.Equal(t, http.StatusCreated, code)
		require.Nil(t, got.TypeID)
		require.Nil(t, got.ItemID)
		require.Equal(t, idp(tree.catGroup), got.AssigneeGroupID)
	})
	t.Run("switch on: the original's type is kept and routes the follow-up to the same queue", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		tree := buildRoutedTree(t, h)
		setChoice(t, h, admin.KeyGuestSubmissionEnabled, true)
		setChoice(t, h, guestsTypeItemKey, true)
		orig, token := closedFor(t, h, tree.choiceTree, true)
		code, got := guestFollowUp(t, h, token)
		require.Equal(t, http.StatusCreated, code)
		require.Equal(t, orig.TypeID, got.TypeID)
		require.Equal(t, idp(tree.typeGroup), got.AssigneeGroupID)
		require.Equal(t, orig.ItemID, got.ItemID, "and the item, while guests are shown items")
	})
	t.Run("types shown but not items: the type is kept and the item is not", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		tree := buildRoutedTree(t, h)
		setChoice(t, h, admin.KeyGuestSubmissionEnabled, true)
		setChoice(t, h, guestsTypesKey, true)
		orig, token := closedFor(t, h, tree.choiceTree, true)
		code, got := guestFollowUp(t, h, token)
		require.Equal(t, http.StatusCreated, code)
		require.Equal(t, orig.TypeID, got.TypeID)
		require.Nil(t, got.ItemID)
	})
	t.Run("switch on, a guest ticket with no type: still none", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		setChoice(t, h, guestsTypeItemKey, true)
		orig, token := seedGuestTicket(t, h)
		require.NoError(t, h.ticketSvc.Close(context.Background(), orig.ID, ticket.Actor{UserID: &h.adminID, Role: user.RoleAdmin}))
		code, got := guestFollowUp(t, h, token)
		require.Equal(t, http.StatusCreated, code)
		require.Nil(t, got.TypeID)
	})
	t.Run("the type has been archived since: the category alone, silently", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		ctx := context.Background()
		tree := buildRoutedTree(t, h)
		setChoice(t, h, admin.KeyGuestSubmissionEnabled, true)
		setChoice(t, h, guestsTypeItemKey, true)
		_, token := closedFor(t, h, tree.choiceTree, true)
		ty, err := h.categorySvc.GetType(ctx, tree.typ)
		require.NoError(t, err)
		ty.Active = false
		require.NoError(t, h.categorySvc.UpdateType(ctx, ty))
		code, got := guestFollowUp(t, h, token)
		require.Equal(t, http.StatusCreated, code)
		require.Nil(t, got.TypeID)
		require.Nil(t, got.ItemID)
		require.Equal(t, idp(tree.catGroup), got.AssigneeGroupID, "routed on the category, as the type is gone")
	})
	t.Run("only the item has been archived since: the type is kept", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		ctx := context.Background()
		tree := buildRoutedTree(t, h)
		setChoice(t, h, admin.KeyGuestSubmissionEnabled, true)
		setChoice(t, h, guestsTypeItemKey, true)
		_, token := closedFor(t, h, tree.choiceTree, true)
		it, err := h.categorySvc.GetItem(ctx, tree.item)
		require.NoError(t, err)
		it.Active = false
		require.NoError(t, h.categorySvc.UpdateItem(ctx, it))
		code, got := guestFollowUp(t, h, token)
		require.Equal(t, http.StatusCreated, code)
		require.Equal(t, idp(tree.typ), got.TypeID)
		require.Nil(t, got.ItemID)
		require.Equal(t, idp(tree.typeGroup), got.AssigneeGroupID)
	})
}

func mustFirst(tk ticket.Ticket, _ string) ticket.Ticket { return tk }
