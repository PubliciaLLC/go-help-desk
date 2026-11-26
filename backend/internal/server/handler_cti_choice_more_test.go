package server_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// #711, the cases the first pass left thin.

// Staff are unaffected by either switch: with both on, as with both off, they
// file under the item, set the priority and file for someone else.
func TestCTIChoice_StaffAreUnaffectedByEitherSwitch(t *testing.T) {
	for _, on := range []bool{false, true} {
		h, cleanup := newHarness(t)
		tree := buildChoiceTree(t, h)
		setChoice(t, h, admin.KeyGuestSubmissionEnabled, on)
		setChoice(t, h, usersItemKey, on)
		setChoice(t, h, guestsTypeItemKey, on)

		res := h.do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
			"subject": "s", "description": "d", "category_id": tree.cat.String(),
			"type_id": tree.typ.String(), "item_id": tree.item.String(), "priority": "critical",
			"reporter_user_id": h.userID.String()})
		body := readAll(t, res)
		require.Equal(t, http.StatusCreated, res.StatusCode, "switches %v: %s", on, body)
		var got struct {
			createdTicket
			Priority string `json:"priority"`
		}
		require.NoError(t, json.Unmarshal(body, &got))
		require.NotNil(t, got.ItemID, "staff always choose the item")
		require.Equal(t, "critical", got.Priority, "and the priority")

		// An item of another type is the same refusal for staff as ever.
		res = h.do(t, http.MethodPost, "/api/v1/tickets", map[string]any{
			"subject": "s", "description": "d", "category_id": tree.cat.String(),
			"type_id": tree.typ.String(), "item_id": tree.otherItem.String()})
		require.Equal(t, http.StatusBadRequest, res.StatusCode, "switches %v", on)
		res.Body.Close()
		cleanup()
	}
}

// A requester's API key with both switches on is held to the requester rules:
// the item is theirs to choose, the priority and the reporter are not.
func TestCTIChoice_RequesterKeyWithBothSwitchesOn(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tree := buildChoiceTree(t, h)
	setChoice(t, h, admin.KeyGuestSubmissionEnabled, true)
	setChoice(t, h, usersItemKey, true)
	setChoice(t, h, guestsTypeItemKey, true)

	code, body := requesterCreate(t, h, map[string]any{
		"category_id": tree.cat.String(), "type_id": tree.typ.String(), "item_id": tree.item.String(), "priority": "critical"})
	require.Equal(t, http.StatusCreated, code, string(body))
	var got struct {
		createdTicket
		Priority string `json:"priority"`
	}
	require.NoError(t, json.Unmarshal(body, &got))
	require.NotNil(t, got.ItemID)
	require.Equal(t, "medium", got.Priority)

	code, _ = requesterCreate(t, h, map[string]any{"category_id": tree.cat.String(), "reporter_email": "x@example.test"})
	require.Equal(t, http.StatusForbidden, code)
	code, _ = requesterCreate(t, h, map[string]any{
		"category_id": tree.cat.String(), "type_id": tree.typ.String(), "item_id": tree.otherItem.String()})
	require.Equal(t, http.StatusBadRequest, code)
}

// The same through a real session, which is what the browser sends: the setting
// is read afresh on each request, and /me reports it.
func TestCTIChoice_RequesterSession(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tree := buildChoiceTree(t, h)
	cookie := h.session(t, asReporter)
	create := func() (int, []byte) {
		res := h.doSession(t, cookie, http.MethodPost, "/api/v1/tickets", map[string]any{
			"subject": "Dock dead", "description": "x", "category_id": tree.cat.String(),
			"type_id": tree.typ.String(), "item_id": tree.item.String()})
		return res.StatusCode, readAll(t, res)
	}

	code, body := create()
	require.Equal(t, http.StatusCreated, code, string(body))
	require.Nil(t, parseCreated(t, body).ItemID, "off: ignored")

	setChoice(t, h, usersItemKey, true)
	code, body = create()
	require.Equal(t, http.StatusCreated, code, string(body))
	require.NotNil(t, parseCreated(t, body).ItemID, "on: kept")

	res := h.doSession(t, cookie, http.MethodGet, "/api/v1/me", nil)
	var me map[string]any
	require.NoError(t, json.Unmarshal(readAll(t, res), &me))
	require.Equal(t, true, me["can_choose_item"])
}

// A null or empty type_id is no choice, and a value of the wrong JSON type is a
// 400 that spends no ticket.
func TestCTIChoice_GuestOn_NullEmptyAndNumericIds(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tree := buildChoiceTree(t, h)
	setChoice(t, h, admin.KeyGuestSubmissionEnabled, true)
	setChoice(t, h, guestsTypeItemKey, true)

	for name, v := range map[string]any{"null": nil, "an empty string": ""} {
		for _, key := range []string{"type_id", "item_id"} {
			code, body := guestPost(t, h, map[string]any{"category_id": tree.cat.String(), key: v})
			require.Equal(t, http.StatusCreated, code, "%s %s: %s", key, name, body)
			typ, item := guestTicketByTracking(t, h, body)
			require.Nil(t, typ, name)
			require.Nil(t, item, name)
		}
	}
	before := ticketCount(t, h)
	for _, key := range []string{"type_id", "item_id"} {
		code, body := guestPost(t, h, map[string]any{"category_id": tree.cat.String(), key: 42})
		require.Equal(t, http.StatusBadRequest, code, "%s: %s", key, body)
	}
	require.Equal(t, before, ticketCount(t, h))
}

// The words a guest is told are the ticket service's, which DESIGN.md quotes.
func TestCTIChoice_GuestOn_RefusalWording(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tree := buildChoiceTree(t, h)
	setChoice(t, h, admin.KeyGuestSubmissionEnabled, true)
	setChoice(t, h, guestsTypeItemKey, true)

	for name, tc := range map[string]struct {
		fields map[string]any
		want   string
	}{
		"an archived type":           {map[string]any{"type_id": tree.archTyp.String()}, "type_id is not an active type"},
		"an archived item":           {map[string]any{"type_id": tree.typ.String(), "item_id": tree.archItem.String()}, "item_id is not an active item"},
		"a type of another category": {map[string]any{"type_id": tree.foreignTyp.String()}, "must belong to the category"},
		"an unknown type":            {map[string]any{"type_id": uuid.NewString()}, "must belong to the category"},
		"an item with no type":       {map[string]any{"item_id": tree.item.String()}, "must belong to the category"},
	} {
		tc.fields["category_id"] = tree.cat.String()
		code, body := guestPost(t, h, tc.fields)
		require.Equal(t, http.StatusBadRequest, code, "%s: %s", name, body)
		require.Contains(t, string(body), tc.want, name)
	}
}
