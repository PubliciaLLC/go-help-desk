package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/catalogue"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// #711: three independent switches, all off by default, that let a registered
// requester see the Item (users_show_items), and a guest the Type
// (guests_show_types) and the Item under it (guests_show_items). The wire names are spelled out here on purpose:
// a renamed constant must not silently rename what administrators and the
// OpenAPI spec see.
const (
	usersItemKey   = "users_show_items"
	guestsTypesKey = "guests_show_types"
	guestsItemsKey = "guests_show_items"
	// guestsTypeItemKey is not a setting: it stands for both guest levels, so a
	// test that wants guests to see the whole tree says so once.
	guestsTypeItemKey = "both guest levels"
)

// choiceTree is a catalogue with one of everything a hostile or stale id can
// point at: a type and item that belong together, a type of another category,
// an item of another type, and one retired node at each level below the
// category.
type choiceTree struct {
	cat, otherCat      uuid.UUID
	typ, otherTyp      uuid.UUID // otherTyp is under cat too
	foreignTyp         uuid.UUID // under otherCat
	item, otherItem    uuid.UUID // item is under typ, otherItem under otherTyp
	foreignItem        uuid.UUID // under foreignTyp
	archTyp, archItem  uuid.UUID // archTyp under cat; archItem under typ
	itemUnderArchTyp   uuid.UUID
	itemName, typeName string
}

func buildChoiceTree(t *testing.T, h *harness) choiceTree {
	t.Helper()
	ctx := context.Background()
	var c choiceTree
	cat, err := h.categorySvc.CreateCategory(ctx, "Hardware", 90)
	require.NoError(t, err)
	other, err := h.categorySvc.CreateCategory(ctx, "Software", 91)
	require.NoError(t, err)
	typ, err := h.categorySvc.CreateType(ctx, cat.ID, "Laptop", 1)
	require.NoError(t, err)
	otherTyp, err := h.categorySvc.CreateType(ctx, cat.ID, "Monitor", 2)
	require.NoError(t, err)
	foreign, err := h.categorySvc.CreateType(ctx, other.ID, "Licence", 1)
	require.NoError(t, err)
	archTyp, err := h.categorySvc.CreateType(ctx, cat.ID, "Retired type", 3)
	require.NoError(t, err)
	item, err := h.categorySvc.CreateItem(ctx, typ.ID, "Docking station", 1)
	require.NoError(t, err)
	otherItem, err := h.categorySvc.CreateItem(ctx, otherTyp.ID, "Cable", 1)
	require.NoError(t, err)
	foreignItem, err := h.categorySvc.CreateItem(ctx, foreign.ID, "Seat", 1)
	require.NoError(t, err)
	archItem, err := h.categorySvc.CreateItem(ctx, typ.ID, "Retired item", 2)
	require.NoError(t, err)
	underArch, err := h.categorySvc.CreateItem(ctx, archTyp.ID, "Item under retired type", 1)
	require.NoError(t, err)

	ai, err := h.categorySvc.GetItem(ctx, archItem.ID)
	require.NoError(t, err)
	ai.Active = false
	require.NoError(t, h.categorySvc.UpdateItem(ctx, ai))
	at, err := h.categorySvc.GetType(ctx, archTyp.ID)
	require.NoError(t, err)
	at.Active = false
	require.NoError(t, h.categorySvc.UpdateType(ctx, at))

	c = choiceTree{
		cat: cat.ID, otherCat: other.ID, typ: typ.ID, otherTyp: otherTyp.ID, foreignTyp: foreign.ID,
		item: item.ID, otherItem: otherItem.ID, foreignItem: foreignItem.ID,
		archTyp: archTyp.ID, archItem: archItem.ID, itemUnderArchTyp: underArch.ID,
		itemName: "Docking station", typeName: "Laptop",
	}
	return c
}

func setChoice(t *testing.T, h *harness, key string, on bool) {
	t.Helper()
	ctx := context.Background()
	if key == guestsTypeItemKey {
		require.NoError(t, h.adminSvc.SetBool(ctx, guestsTypesKey, on))
		require.NoError(t, h.adminSvc.SetBool(ctx, guestsItemsKey, on))
		return
	}
	require.NoError(t, h.adminSvc.SetBool(ctx, key, on))
}

// levels is what the one policy says a caller sees, read the way every surface
// reads it.
func levels(t *testing.T, h *harness, who catalogue.Who) catalogue.Levels {
	t.Helper()
	return catalogue.VisibleLevels(context.Background(), h.adminSvc, who)
}

func idp(id uuid.UUID) *uuid.UUID { return &id }

// ── The settings ─────────────────────────────────────────────────────────────

var showKeys = []string{usersItemKey, guestsTypesKey, guestsItemsKey}

// Off by default, on an upgraded install as on a fresh one: none of the rows
// exists after the migrations have run, an absent row reads false, and nobody
// but staff sees anything below the category. There is no migration to
// renumber and nothing for an upgrade to get wrong.
func TestCTIChoice_AbsentMeansOff(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	setChoice(t, h, admin.KeyGuestSubmissionEnabled, true)

	all, err := h.adminSvc.ListAll(ctx)
	require.NoError(t, err)
	for _, k := range showKeys {
		require.NotContains(t, all, k, "no migration seeds it: absent is the default")
	}

	require.Equal(t, catalogue.Levels{Types: true}, levels(t, h, catalogue.Requester), "types always, items off")
	require.Equal(t, catalogue.Levels{}, levels(t, h, catalogue.Guest))
	require.Equal(t, catalogue.Levels{Types: true, Items: true}, levels(t, h, catalogue.Staff))

	sess := signInAsAdmin(t, h)
	res, body := sess.send(t, http.MethodGet, "/api/v1/admin/settings", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	for _, k := range showKeys {
		require.NotContains(t, string(body), k)
	}
}

func TestCTIChoice_NamesAreKnownKeys(t *testing.T) {
	require.Equal(t, usersItemKey, admin.KeyUsersShowItems)
	require.Equal(t, guestsTypesKey, admin.KeyGuestsShowTypes)
	require.Equal(t, guestsItemsKey, admin.KeyGuestsShowItems)
	for _, k := range showKeys {
		require.True(t, admin.IsKnownKey(k), k)
	}
}

// An administrator session saves and reads back each switch; they do not read
// one another, except that the guest Item level hangs under the guest Type level.
func TestCTIChoice_AdminSessionSetsEachSwitchIndependently(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	setChoice(t, h, admin.KeyGuestSubmissionEnabled, true)
	sess := signInAsAdmin(t, h)
	patch := func(m map[string]any) {
		res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", m)
		require.Equal(t, http.StatusNoContent, res.StatusCode, body)
	}

	patch(map[string]any{usersItemKey: true})
	require.Equal(t, catalogue.Levels{Types: true, Items: true}, levels(t, h, catalogue.Requester))
	require.Equal(t, catalogue.Levels{}, levels(t, h, catalogue.Guest), "the user switch does not open the guest levels")

	patch(map[string]any{guestsTypesKey: true, usersItemKey: false})
	require.Equal(t, catalogue.Levels{Types: true}, levels(t, h, catalogue.Requester))
	require.Equal(t, catalogue.Levels{Types: true}, levels(t, h, catalogue.Guest))

	patch(map[string]any{guestsItemsKey: true})
	require.Equal(t, catalogue.Levels{Types: true, Items: true}, levels(t, h, catalogue.Guest))

	res, body := sess.send(t, http.MethodGet, "/api/v1/admin/settings", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	var dump map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &dump))
	require.JSONEq(t, "true", string(dump[guestsTypesKey]))
	require.JSONEq(t, "true", string(dump[guestsItemsKey]))
	require.JSONEq(t, "false", string(dump[usersItemKey]))
}

// The guest levels are effective only while guest submission is on, and the
// Item level only under a Type level; the stored values are kept and an
// inconsistent stored combination reads as the consistent one.
func TestCTIChoice_GuestLevelsAreDependent(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	setChoice(t, h, guestsTypeItemKey, true)

	require.Equal(t, catalogue.Levels{}, levels(t, h, catalogue.Guest), "submission is off")
	setChoice(t, h, admin.KeyGuestSubmissionEnabled, true)
	require.Equal(t, catalogue.Levels{Types: true, Items: true}, levels(t, h, catalogue.Guest))

	setChoice(t, h, guestsTypesKey, false)
	require.Equal(t, catalogue.Levels{}, levels(t, h, catalogue.Guest), "items on, types off is items off")
	raw, err := h.adminSvc.GetRaw(ctx, guestsItemsKey)
	require.NoError(t, err)
	require.JSONEq(t, "true", string(raw), "and the stored value is kept")

	setChoice(t, h, guestsTypesKey, true)
	setChoice(t, h, admin.KeyGuestSubmissionEnabled, false)
	require.Equal(t, catalogue.Levels{}, levels(t, h, catalogue.Guest))
	raw, err = h.adminSvc.GetRaw(ctx, guestsTypesKey)
	require.NoError(t, err)
	require.JSONEq(t, "true", string(raw), "turning submission off does not lose the choice")
}

// A value that is not a JSON boolean is refused and nothing is stored: a string
// "true" would read back as off and the administrator would believe they had
// chosen.
func TestCTIChoice_SettingsRefuseNonBooleans(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	sess := signInAsAdmin(t, h)

	for _, key := range showKeys {
		for name, v := range map[string]any{"a string": "true", "null": nil, "a number": 1, "an object": map[string]any{}} {
			res, body := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{key: v})
			require.Equal(t, http.StatusBadRequest, res.StatusCode, "%s %s: %s", key, name, body)
		}
		_, err := h.adminSvc.GetRaw(ctx, key)
		require.Error(t, err, "%s: nothing was stored", key)
	}
}

// Session only: a leaked API key must not open any of them.
func TestCTIChoice_SettingsAreSessionOnly(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	for _, key := range showKeys {
		require.Contains(t, admin.AuthCriticalKeys(), key)
		res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{key: true})
		body := string(readAll(t, res))
		require.Equal(t, http.StatusForbidden, res.StatusCode, body)
		require.Equal(t, "session_required", errCode(t, []byte(body)))
		_, err := h.adminSvc.GetRaw(ctx, key)
		require.Error(t, err, "the refused write stored nothing")
	}
}

// ── Creating a ticket as a requester ─────────────────────────────────────────

func requesterCreate(t *testing.T, h *harness, fields map[string]any) (int, []byte) {
	t.Helper()
	body := map[string]any{"subject": "Dock dead", "description": "x"}
	for k, v := range fields {
		body[k] = v
	}
	res := h.doAsUser(t, http.MethodPost, "/api/v1/tickets", body)
	return res.StatusCode, readAll(t, res)
}

type createdTicket struct {
	ID             string  `json:"id"`
	TrackingNumber string  `json:"tracking_number"`
	TypeID         *string `json:"type_id"`
	ItemID         *string `json:"item_id"`
}

func parseCreated(t *testing.T, body []byte) createdTicket {
	t.Helper()
	var c createdTicket
	require.NoError(t, json.Unmarshal(body, &c), string(body))
	return c
}

// Off: exactly today. The item is dropped without a word, the type is held to
// the rules it always was.
func TestCTIChoice_RequesterOff_ItemIsIgnoredTypeIsKept(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tree := buildChoiceTree(t, h)

	code, body := requesterCreate(t, h, map[string]any{
		"category_id": tree.cat.String(), "type_id": tree.typ.String(), "item_id": tree.item.String()})
	require.Equal(t, http.StatusCreated, code, string(body))
	got := parseCreated(t, body)
	require.NotNil(t, got.TypeID)
	require.Equal(t, tree.typ.String(), *got.TypeID)
	require.Nil(t, got.ItemID, "the item is not taken from a requester while the setting is off")

	// Ignored, not validated: even a made-up item id is dropped, as it always was.
	code, body = requesterCreate(t, h, map[string]any{
		"category_id": tree.cat.String(), "type_id": tree.typ.String(), "item_id": uuid.NewString()})
	require.Equal(t, http.StatusCreated, code, string(body))
	require.Nil(t, parseCreated(t, body).ItemID)

	// The type rules are untouched.
	for name, f := range map[string]map[string]any{
		"a type of another category": {"category_id": tree.cat.String(), "type_id": tree.foreignTyp.String()},
		"an archived type":           {"category_id": tree.cat.String(), "type_id": tree.archTyp.String()},
	} {
		code, body = requesterCreate(t, h, f)
		require.Equal(t, http.StatusBadRequest, code, "%s: %s", name, body)
	}
}

func TestCTIChoice_RequesterOn_ItemIsStored(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tree := buildChoiceTree(t, h)
	setChoice(t, h, usersItemKey, true)

	code, body := requesterCreate(t, h, map[string]any{
		"category_id": tree.cat.String(), "type_id": tree.typ.String(), "item_id": tree.item.String()})
	require.Equal(t, http.StatusCreated, code, string(body))
	got := parseCreated(t, body)
	require.NotNil(t, got.ItemID)
	require.Equal(t, tree.item.String(), *got.ItemID)

	stored, err := h.ticketSvc.GetByTrackingNumber(context.Background(), tracking(got.TrackingNumber))
	require.NoError(t, err)
	require.Equal(t, idp(tree.item), stored.ItemID)

	// Optional all the way down: a type alone, a category alone.
	code, body = requesterCreate(t, h, map[string]any{"category_id": tree.cat.String(), "type_id": tree.typ.String()})
	require.Equal(t, http.StatusCreated, code, string(body))
	require.Nil(t, parseCreated(t, body).ItemID)
	code, body = requesterCreate(t, h, map[string]any{"category_id": tree.cat.String()})
	require.Equal(t, http.StatusCreated, code, string(body))
}

// On: the whole chain is held, a refusal is a 400 and never a 500, and a
// refused request spends no tracking number.
func TestCTIChoice_RequesterOn_HostileIdsAreRefusedBeforeANumberIsSpent(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tree := buildChoiceTree(t, h)
	setChoice(t, h, usersItemKey, true)

	refused := map[string]map[string]any{
		"an item of another type":              {"category_id": tree.cat.String(), "type_id": tree.typ.String(), "item_id": tree.otherItem.String()},
		"an item of another category":          {"category_id": tree.cat.String(), "type_id": tree.typ.String(), "item_id": tree.foreignItem.String()},
		"an item with no type":                 {"category_id": tree.cat.String(), "item_id": tree.item.String()},
		"a type of another category":           {"category_id": tree.cat.String(), "type_id": tree.foreignTyp.String(), "item_id": tree.foreignItem.String()},
		"an unknown item":                      {"category_id": tree.cat.String(), "type_id": tree.typ.String(), "item_id": uuid.NewString()},
		"an unknown type":                      {"category_id": tree.cat.String(), "type_id": uuid.NewString(), "item_id": uuid.NewString()},
		"an archived item":                     {"category_id": tree.cat.String(), "type_id": tree.typ.String(), "item_id": tree.archItem.String()},
		"an archived type":                     {"category_id": tree.cat.String(), "type_id": tree.archTyp.String(), "item_id": tree.itemUnderArchTyp.String()},
		"an item under an archived type":       {"category_id": tree.cat.String(), "type_id": tree.archTyp.String(), "item_id": tree.itemUnderArchTyp.String()},
		"a malformed item id":                  {"category_id": tree.cat.String(), "type_id": tree.typ.String(), "item_id": "not-a-uuid"},
		"a nil item id with a type":            {"category_id": tree.cat.String(), "type_id": tree.typ.String(), "item_id": uuid.Nil.String()},
		"an item from another category's type": {"category_id": tree.otherCat.String(), "type_id": tree.typ.String(), "item_id": tree.item.String()},
	}

	code, body := requesterCreate(t, h, map[string]any{"category_id": tree.cat.String()})
	require.Equal(t, http.StatusCreated, code, string(body))
	first := seqOf(t, parseCreated(t, body).TrackingNumber)
	before := ticketCount(t, h)

	for name, f := range refused {
		code, body := requesterCreate(t, h, f)
		require.Equal(t, http.StatusBadRequest, code, "%s: %s", name, body)
	}
	require.Equal(t, before, ticketCount(t, h), "a refused request writes no ticket")

	code, body = requesterCreate(t, h, map[string]any{"category_id": tree.cat.String()})
	require.Equal(t, http.StatusCreated, code, string(body))
	require.Equal(t, first+1, seqOf(t, parseCreated(t, body).TrackingNumber), "and spends no tracking number")
}

// An archived category is refused for a requester whatever the settings say.
func TestCTIChoice_RequesterOn_StillHeldToAnActiveCategory(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tree := buildChoiceTree(t, h)
	setChoice(t, h, usersItemKey, true)
	c, err := h.categorySvc.GetCategory(ctx, tree.cat)
	require.NoError(t, err)
	c.Active = false
	require.NoError(t, h.categorySvc.UpdateCategory(ctx, c))

	code, body := requesterCreate(t, h, map[string]any{
		"category_id": tree.cat.String(), "type_id": tree.typ.String(), "item_id": tree.item.String()})
	require.Equal(t, http.StatusBadRequest, code, string(body))
}

// The switch is about a requester; it grants nothing else and changes nothing
// for staff.
func TestCTIChoice_RequesterOn_StillCannotSetPriorityOrReporter(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tree := buildChoiceTree(t, h)
	setChoice(t, h, usersItemKey, true)

	code, body := requesterCreate(t, h, map[string]any{
		"category_id": tree.cat.String(), "type_id": tree.typ.String(), "item_id": tree.item.String(), "priority": "critical"})
	require.Equal(t, http.StatusCreated, code, string(body))
	var got struct {
		Priority string `json:"priority"`
	}
	require.NoError(t, json.Unmarshal(body, &got))
	require.Equal(t, "medium", got.Priority)

	code, body = requesterCreate(t, h, map[string]any{
		"category_id": tree.cat.String(), "reporter_user_id": h.staffID.String()})
	require.Equal(t, http.StatusForbidden, code, string(body))
}

// Custom fields keyed by the item resolve for the chosen item, for a requester
// exactly as for staff: required where they say so, saved when sent.
func TestCTIChoice_RequesterOn_ItemFieldsFollowTheSelection(t *testing.T) {
	for _, on := range []bool{false, true} {
		name := "off"
		if on {
			name = "on"
		}
		t.Run(name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			ctx := context.Background()
			tree := buildChoiceTree(t, h)
			setChoice(t, h, usersItemKey, on)
			def := h.mkField(t, "Dock serial", customfield.FieldTypeText, nil, 1)
			_, err := h.customFieldSvc.Attach(ctx, customfield.Assignment{
				FieldDefID: def.ID, ScopeType: customfield.ItemScope, ScopeID: tree.item, VisibleOnNew: true, RequiredOnNew: true,
			}, nil)
			require.NoError(t, err)

			pick := map[string]any{"category_id": tree.cat.String(), "type_id": tree.typ.String(), "item_id": tree.item.String()}
			if !on {
				// The item is dropped, so its required field is not this ticket's.
				code, body := requesterCreate(t, h, pick)
				require.Equal(t, http.StatusCreated, code, string(body))
				return
			}
			code, body := requesterCreate(t, h, pick)
			require.Equal(t, http.StatusBadRequest, code, string(body))
			require.Contains(t, string(body), "Dock serial")

			pick["custom_fields"] = map[string]string{def.ID.String(): "SN-9"}
			code, body = requesterCreate(t, h, pick)
			require.Equal(t, http.StatusCreated, code, string(body))
			require.Equal(t, map[string]string{def.ID.String(): "SN-9"}, h.storedValues(t, parseCreated(t, body).ID))
		})
	}
}

// ── Creating a ticket as a guest ─────────────────────────────────────────────

func guestPost(t *testing.T, h *harness, fields map[string]any) (int, []byte) {
	t.Helper()
	body := map[string]any{
		"subject": "Dock dead", "description": "x",
		"guest_email": "visitor@example.test", "guest_name": "Visitor",
	}
	for k, v := range fields {
		body[k] = v
	}
	res := h.doGuest(t, http.MethodPost, "/api/v1/guest/tickets", "", body)
	return res.StatusCode, readAll(t, res)
}

func guestTicketByTracking(t *testing.T, h *harness, body []byte) (typ, item *uuid.UUID) {
	t.Helper()
	var got struct {
		TrackingNumber string `json:"tracking_number"`
	}
	require.NoError(t, json.Unmarshal(body, &got), string(body))
	tk, err := h.ticketSvc.GetByTrackingNumber(context.Background(), tracking(got.TrackingNumber))
	require.NoError(t, err)
	return tk.TypeID, tk.ItemID
}

// Off: exactly today. The ids are unknown keys to the guest form and are
// ignored, even a made-up one that would fail a foreign key if it were read.
func TestCTIChoice_GuestOff_TypeAndItemAreIgnored(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tree := buildChoiceTree(t, h)
	setChoice(t, h, admin.KeyGuestSubmissionEnabled, true)

	for name, extra := range map[string]map[string]any{
		"a real type and item": {"type_id": tree.typ.String(), "item_id": tree.item.String()},
		"made-up ids":          {"type_id": uuid.NewString(), "item_id": uuid.NewString()},
	} {
		extra["category_id"] = tree.cat.String()
		code, body := guestPost(t, h, extra)
		require.Equal(t, http.StatusCreated, code, "%s: %s", name, body)
		typ, item := guestTicketByTracking(t, h, body)
		require.Nil(t, typ, name)
		require.Nil(t, item, name)
	}
}

// On, but guest submission off: the route is the 404 it always was.
func TestCTIChoice_GuestOnWithSubmissionOff_IsTheSame404(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tree := buildChoiceTree(t, h)
	setChoice(t, h, guestsTypeItemKey, true)

	code, body := guestPost(t, h, map[string]any{"category_id": tree.cat.String(), "type_id": tree.typ.String()})
	require.Equal(t, http.StatusNotFound, code, string(body))
}

func TestCTIChoice_GuestOn_TypeAndItemAreStored(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tree := buildChoiceTree(t, h)
	setChoice(t, h, admin.KeyGuestSubmissionEnabled, true)
	setChoice(t, h, guestsTypeItemKey, true)

	code, body := guestPost(t, h, map[string]any{
		"category_id": tree.cat.String(), "type_id": tree.typ.String(), "item_id": tree.item.String()})
	require.Equal(t, http.StatusCreated, code, string(body))
	typ, item := guestTicketByTracking(t, h, body)
	require.Equal(t, idp(tree.typ), typ)
	require.Equal(t, idp(tree.item), item)

	code, body = guestPost(t, h, map[string]any{"category_id": tree.cat.String(), "type_id": tree.typ.String()})
	require.Equal(t, http.StatusCreated, code, string(body))
	typ, item = guestTicketByTracking(t, h, body)
	require.Equal(t, idp(tree.typ), typ)
	require.Nil(t, item)

	// Neither is required: a category alone still files.
	code, body = guestPost(t, h, map[string]any{"category_id": tree.cat.String()})
	require.Equal(t, http.StatusCreated, code, string(body))
	typ, item = guestTicketByTracking(t, h, body)
	require.Nil(t, typ)
	require.Nil(t, item)
}

func TestCTIChoice_GuestOn_HostileIdsAreRefusedBeforeANumberIsSpent(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tree := buildChoiceTree(t, h)
	setChoice(t, h, admin.KeyGuestSubmissionEnabled, true)
	setChoice(t, h, guestsTypeItemKey, true)

	refused := map[string]map[string]any{
		"an item of another type":        {"category_id": tree.cat.String(), "type_id": tree.typ.String(), "item_id": tree.otherItem.String()},
		"an item with no type":           {"category_id": tree.cat.String(), "item_id": tree.item.String()},
		"a type of another category":     {"category_id": tree.cat.String(), "type_id": tree.foreignTyp.String()},
		"an unknown type":                {"category_id": tree.cat.String(), "type_id": uuid.NewString()},
		"an unknown item":                {"category_id": tree.cat.String(), "type_id": tree.typ.String(), "item_id": uuid.NewString()},
		"an archived type":               {"category_id": tree.cat.String(), "type_id": tree.archTyp.String()},
		"an item under an archived type": {"category_id": tree.cat.String(), "type_id": tree.archTyp.String(), "item_id": tree.itemUnderArchTyp.String()},
		"an archived item":               {"category_id": tree.cat.String(), "type_id": tree.typ.String(), "item_id": tree.archItem.String()},
		"a malformed type id":            {"category_id": tree.cat.String(), "type_id": "not-a-uuid"},
	}

	code, body := guestPost(t, h, map[string]any{"category_id": tree.cat.String()})
	require.Equal(t, http.StatusCreated, code, string(body))
	first := seqOf(t, trackingOf(t, body))
	before := ticketCount(t, h)

	for name, f := range refused {
		code, body := guestPost(t, h, f)
		require.Equal(t, http.StatusBadRequest, code, "%s: %s", name, body)
	}
	require.Equal(t, before, ticketCount(t, h), "a refused request writes no ticket")

	code, body = guestPost(t, h, map[string]any{"category_id": tree.cat.String()})
	require.Equal(t, http.StatusCreated, code, string(body))
	require.Equal(t, first+1, seqOf(t, trackingOf(t, body)), "and spends no tracking number")
}

// A guest still cannot set what the form never offered.
func TestCTIChoice_GuestOn_StillCannotSetPriority(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tree := buildChoiceTree(t, h)
	setChoice(t, h, admin.KeyGuestSubmissionEnabled, true)
	setChoice(t, h, guestsTypeItemKey, true)

	code, body := guestPost(t, h, map[string]any{
		"category_id": tree.cat.String(), "type_id": tree.typ.String(), "priority": "critical"})
	require.Equal(t, http.StatusCreated, code, string(body))
	var got struct {
		TrackingNumber string `json:"tracking_number"`
	}
	require.NoError(t, json.Unmarshal(body, &got))
	tk, err := h.ticketSvc.GetByTrackingNumber(context.Background(), tracking(got.TrackingNumber))
	require.NoError(t, err)
	require.Equal(t, "medium", string(tk.Priority))
}

// The fields a guest is offered stay the category's own: choosing a type or an
// item changes descriptions and routing, not which fields are asked.
func TestCTIChoice_GuestFieldsStayTheCategorys(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	tree := buildChoiceTree(t, h)
	setChoice(t, h, admin.KeyGuestSubmissionEnabled, true)
	setChoice(t, h, guestsTypeItemKey, true)

	def := h.mkField(t, "Dock serial", customfield.FieldTypeText, nil, 1)
	_, err := h.customFieldSvc.Attach(ctx, customfield.Assignment{
		FieldDefID: def.ID, ScopeType: customfield.ItemScope, ScopeID: tree.item, VisibleOnNew: true, RequiredOnNew: true, VisibleToGuests: true,
	}, nil)
	require.Error(t, err, "a guest-visible field on an item is refused, as it always was")

	code, body := h.guestFields(t, tree.cat.String())
	require.Equal(t, http.StatusOK, code, string(body))
	require.JSONEq(t, "[]", string(body))
}

// ── The catalogue routes ─────────────────────────────────────────────────────

func listNames(t *testing.T, res *http.Response) []string {
	t.Helper()
	var rows []struct {
		Name string `json:"name"`
	}
	body := readAll(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))
	require.NoError(t, json.Unmarshal(body, &rows), string(body))
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out
}

func (c choiceTree) typesPath() string { return "/api/v1/categories/" + c.cat.String() + "/types" }
func (c choiceTree) itemsPath() string {
	return "/api/v1/categories/" + c.cat.String() + "/types/" + c.typ.String() + "/items"
}

// A guest reads nothing below the category while the guest levels are off, only
// the types while just Types is on, and no item name is in either answer.
func TestCTIChoice_CatalogueForGuests(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tree := buildChoiceTree(t, h)
	setChoice(t, h, admin.KeyGuestSubmissionEnabled, true)

	empty := func(path string) {
		t.Helper()
		res := h.doUnauth(t, http.MethodGet, path, nil)
		body := string(readAll(t, res))
		require.Equal(t, http.StatusOK, res.StatusCode, body)
		require.JSONEq(t, "[]", body, path)
		require.NotContains(t, body, tree.itemName)
	}

	// Off: categories as ever, then nothing, in the spelling an archived parent
	// already uses.
	require.Contains(t, listNames(t, h.doUnauth(t, http.MethodGet, "/api/v1/categories", nil)), "Hardware")
	empty(tree.typesPath())
	empty(tree.itemsPath())

	// Types only: the types, still no items.
	setChoice(t, h, guestsTypesKey, true)
	require.ElementsMatch(t, []string{"Laptop", "Monitor"}, listNames(t, h.doUnauth(t, http.MethodGet, tree.typesPath(), nil)), "the archived type is not listed")
	empty(tree.itemsPath())

	// Items on with Types off is Items off.
	setChoice(t, h, guestsTypesKey, false)
	setChoice(t, h, guestsItemsKey, true)
	empty(tree.typesPath())
	empty(tree.itemsPath())

	// Both: the active rows, and only those.
	setChoice(t, h, guestsTypesKey, true)
	items := listNames(t, h.doUnauth(t, http.MethodGet, tree.itemsPath(), nil))
	require.Equal(t, []string{"Docking station"}, items, "the archived item is not listed")

	// Under an archived type a guest reads nothing, as before.
	res := h.doUnauth(t, http.MethodGet, "/api/v1/categories/"+tree.cat.String()+"/types/"+tree.archTyp.String()+"/items", nil)
	require.Empty(t, listNames(t, res))
}

// With guest submission off the routes stay behind a sign-in, whatever the
// guest switch says.
func TestCTIChoice_CatalogueGuestSwitchDoesNotOpenAClosedInstance(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tree := buildChoiceTree(t, h)
	setChoice(t, h, guestsTypeItemKey, true)

	for _, p := range []string{"/api/v1/categories", tree.typesPath(), tree.itemsPath()} {
		res := h.doUnauth(t, http.MethodGet, p, nil)
		require.Equal(t, http.StatusUnauthorized, res.StatusCode, p)
	}
}

// A signed-in requester reads the categories and types always, and the items
// only while Items is shown to registered users: `[]` otherwise, never a refusal.
func TestCTIChoice_CatalogueForRequesters(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tree := buildChoiceTree(t, h)

	for _, on := range []bool{false, true} {
		setChoice(t, h, usersItemKey, on)
		require.ElementsMatch(t, []string{"Laptop", "Monitor"}, listNames(t, h.doAsUser(t, http.MethodGet, tree.typesPath(), nil)), "switch %v", on)
		res := h.doAsUser(t, http.MethodGet, tree.itemsPath(), nil)
		if on {
			require.Equal(t, []string{"Docking station"}, listNames(t, res))
		} else {
			require.Empty(t, listNames(t, res))
		}
	}
}

// Staff and administrators see the whole tree, archived rows included, whatever
// either switch says.
func TestCTIChoice_CatalogueForStaffIsUnchanged(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tree := buildChoiceTree(t, h)

	items := listNames(t, h.do(t, http.MethodGet, tree.itemsPath(), nil))
	require.ElementsMatch(t, []string{"Docking station", "Retired item"}, items)
	types := listNames(t, h.doAsAdmin(t, http.MethodGet, tree.typesPath(), nil))
	require.Contains(t, types, "Retired type")
}

// ── What the forms are told ──────────────────────────────────────────────────

// GET /site tells the guest form the effective levels: always present, false by
// default, false while guest submission is off whatever the stored value, and
// Items false while Types is.
func TestCTIChoice_SiteReportsTheEffectiveGuestLevels(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	require.Equal(t, false, siteConfig(t, h)[guestsTypesKey], "present and false by default")
	require.Equal(t, false, siteConfig(t, h)[guestsItemsKey])
	setChoice(t, h, guestsTypeItemKey, true)
	require.Equal(t, false, siteConfig(t, h)[guestsTypesKey], "submission is off")
	require.Equal(t, false, siteConfig(t, h)[guestsItemsKey])
	setChoice(t, h, admin.KeyGuestSubmissionEnabled, true)
	require.Equal(t, true, siteConfig(t, h)[guestsTypesKey])
	require.Equal(t, true, siteConfig(t, h)[guestsItemsKey])
	setChoice(t, h, guestsTypesKey, false)
	require.Equal(t, false, siteConfig(t, h)[guestsItemsKey], "items hang under types")

	// The user switch is for signed-in people: the public document does not carry it.
	setChoice(t, h, usersItemKey, true)
	require.NotContains(t, siteConfig(t, h), usersItemKey)
	require.NotContains(t, siteConfig(t, h), "can_choose_item")
}

// GET /me tells the new-ticket page whether to offer the Item: always for staff
// and administrators, the user switch for a requester.
func TestCTIChoice_MeReportsWhetherTheItemIsOffered(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	canChoose := func(do func(*testing.T, string, string, any) *http.Response) any {
		res := do(t, http.MethodGet, "/api/v1/me", nil)
		body := readAll(t, res)
		require.Equal(t, http.StatusOK, res.StatusCode, string(body))
		var out map[string]any
		require.NoError(t, json.Unmarshal(body, &out))
		v, present := out["can_choose_item"]
		require.True(t, present, "always present: %s", body)
		return v
	}

	require.Equal(t, false, canChoose(h.doAsUser))
	require.Equal(t, true, canChoose(h.do), "staff")
	require.Equal(t, true, canChoose(h.doAsAdmin), "administrators")

	setChoice(t, h, usersItemKey, true)
	require.Equal(t, true, canChoose(h.doAsUser))
	setChoice(t, h, usersItemKey, false)
	require.Equal(t, false, canChoose(h.doAsUser))
	// The guest switch has nothing to do with a signed-in requester.
	setChoice(t, h, guestsTypeItemKey, true)
	require.Equal(t, false, canChoose(h.doAsUser))
}

func tracking(s string) ticket.TrackingNumber { return ticket.TrackingNumber(s) }

// trackingOf reads the tracking number out of a create response.
func trackingOf(t *testing.T, body []byte) string {
	t.Helper()
	var got struct {
		TrackingNumber string `json:"tracking_number"`
	}
	require.NoError(t, json.Unmarshal(body, &got), string(body))
	return got.TrackingNumber
}

// seqOf is the sequence part of GHD-2026-000012, so a test can say two tickets
// were numbered one after the other.
func seqOf(t *testing.T, tn string) int {
	t.Helper()
	n, err := strconv.Atoi(tn[strings.LastIndex(tn, "-")+1:])
	require.NoError(t, err, tn)
	return n
}
