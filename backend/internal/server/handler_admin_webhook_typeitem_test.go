package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/server"
)

// The webhook ticket filter's type and item conditions (#631). Four places each
// silently widen or break a hook if they miss the new fields: the empty-filter
// check (domain and notify tests), the redelivery state, the audit text and
// request validation. The last three are here.

// ctiTree is a second category with a type and an item, beside the harness's
// own category (which has neither).
type ctiTree struct {
	cat2            uuid.UUID
	typ, typOther   uuid.UUID // typ is under h.catID; typOther is under cat2
	item, itemOther uuid.UUID // item is under typ; itemOther is under typOther
}

func (h *harness) makeCTITree(t *testing.T) ctiTree {
	t.Helper()
	ctx := context.Background()
	cat2, err := h.categorySvc.CreateCategory(ctx, "Facilities", 2)
	require.NoError(t, err)
	typ, err := h.categorySvc.CreateType(ctx, h.catID, "Laptop", 1)
	require.NoError(t, err)
	typOther, err := h.categorySvc.CreateType(ctx, cat2.ID, "Door", 1)
	require.NoError(t, err)
	item, err := h.categorySvc.CreateItem(ctx, typ.ID, "Battery", 1)
	require.NoError(t, err)
	itemOther, err := h.categorySvc.CreateItem(ctx, typOther.ID, "Lock", 1)
	require.NoError(t, err)
	return ctiTree{cat2.ID, typ.ID, typOther.ID, item.ID, itemOther.ID}
}

func ids(v ...uuid.UUID) []string {
	out := make([]string, len(v))
	for i, id := range v {
		out[i] = id.String()
	}
	return out
}

// 4. Request validation, on both create and update.
func TestWebhookFilter_TypeAndItemAreAccepted(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	c := h.makeCTITree(t)

	for _, tc := range []struct {
		name   string
		filter map[string]any
		want   notification.WebhookFilter
	}{
		{"a type alone", map[string]any{"type_ids": ids(c.typ)},
			notification.WebhookFilter{TypeIDs: []uuid.UUID{c.typ}}},
		{"an item alone", map[string]any{"item_ids": ids(c.item)},
			notification.WebhookFilter{ItemIDs: []uuid.UUID{c.item}}},
		{"a type inside its ticked category", map[string]any{"category_ids": ids(h.catID), "type_ids": ids(c.typ)},
			notification.WebhookFilter{CategoryIDs: []uuid.UUID{h.catID}, TypeIDs: []uuid.UUID{c.typ}}},
		{"an item inside its ticked type and category",
			map[string]any{"category_ids": ids(h.catID), "type_ids": ids(c.typ), "item_ids": ids(c.item)},
			notification.WebhookFilter{CategoryIDs: []uuid.UUID{h.catID}, TypeIDs: []uuid.UUID{c.typ}, ItemIDs: []uuid.UUID{c.item}}},
		{"types in two ticked categories", map[string]any{"category_ids": ids(h.catID, c.cat2), "type_ids": ids(c.typ, c.typOther)},
			notification.WebhookFilter{CategoryIDs: []uuid.UUID{h.catID, c.cat2}, TypeIDs: []uuid.UUID{c.typ, c.typOther}}},
		{"inverted with only a type (it is a condition)", map[string]any{"type_ids": ids(c.typ), "invert": true},
			notification.WebhookFilter{TypeIDs: []uuid.UUID{c.typ}, Invert: true}},
	} {
		t.Run("create: "+tc.name, func(t *testing.T) {
			res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/webhooks", map[string]any{
				"url": "https://example.com/hook", "events": []string{"*"}, "filter": tc.filter,
			})
			b := readAll(t, res)
			require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", b)
			hooks, err := h.authStore.ListWebhooks(ctx)
			require.NoError(t, err)
			var found bool
			for _, wh := range hooks {
				if wh.Filter.Invert == tc.want.Invert && len(wh.Filter.TypeIDs) == len(tc.want.TypeIDs) && len(wh.Filter.ItemIDs) == len(tc.want.ItemIDs) &&
					len(wh.Filter.CategoryIDs) == len(tc.want.CategoryIDs) {
					found = true
					require.Equal(t, normalizedFilter(tc.want).TypeIDs, normalizedFilter(wh.Filter).TypeIDs)
				}
			}
			require.True(t, found, "the stored hook carries the type and item conditions")
		})
	}

	t.Run("update", func(t *testing.T) {
		id := seedWebhook(t, h)
		res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/webhooks/"+id.String(),
			map[string]any{"filter": map[string]any{"type_ids": ids(c.typ), "item_ids": ids(c.item)}})
		b := readAll(t, res)
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)
		got, err := h.authStore.GetWebhook(ctx, id)
		require.NoError(t, err)
		require.Equal(t, []uuid.UUID{c.typ}, got.Filter.TypeIDs)
		require.Equal(t, []uuid.UUID{c.item}, got.Filter.ItemIDs)
	})
}

func TestWebhookFilter_TypeAndItemRefusals(t *testing.T) {
	tooMany := make([]string, 201)
	for i := range tooMany {
		tooMany[i] = uuid.NewString()
	}
	cases := []struct {
		name   string
		filter func(c ctiTree, catID uuid.UUID) map[string]any
		code   string
	}{
		{"unknown type", func(ctiTree, uuid.UUID) map[string]any { return map[string]any{"type_ids": ids(uuid.New())} }, "unknown_filter_type"},
		{"unknown item", func(ctiTree, uuid.UUID) map[string]any { return map[string]any{"item_ids": ids(uuid.New())} }, "unknown_filter_item"},
		{"malformed type id", func(ctiTree, uuid.UUID) map[string]any { return map[string]any{"type_ids": []string{"nope"}} }, "invalid_filter"},
		{"type list is not an array", func(ctiTree, uuid.UUID) map[string]any { return map[string]any{"type_ids": "x"} }, "invalid_filter"},
		{"too many types", func(ctiTree, uuid.UUID) map[string]any { return map[string]any{"type_ids": tooMany} }, "invalid_filter"},
		{"too many items", func(ctiTree, uuid.UUID) map[string]any { return map[string]any{"item_ids": tooMany} }, "invalid_filter"},
		// D37: a condition that can never be met is refused, not stored.
		{"a type outside the ticked categories", func(c ctiTree, cat uuid.UUID) map[string]any {
			return map[string]any{"category_ids": ids(cat), "type_ids": ids(c.typOther)}
		}, "contradictory_filter"},
		{"one type of two outside the ticked categories", func(c ctiTree, cat uuid.UUID) map[string]any {
			return map[string]any{"category_ids": ids(cat), "type_ids": ids(c.typ, c.typOther)}
		}, "contradictory_filter"},
		{"an item outside the ticked types", func(c ctiTree, cat uuid.UUID) map[string]any {
			return map[string]any{"type_ids": ids(c.typ), "item_ids": ids(c.itemOther)}
		}, "contradictory_filter"},
		{"an item outside the ticked categories", func(c ctiTree, cat uuid.UUID) map[string]any {
			return map[string]any{"category_ids": ids(cat), "item_ids": ids(c.itemOther)}
		}, "contradictory_filter"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			ctx := context.Background()
			c := h.makeCTITree(t)
			filter := tc.filter(c, h.catID)

			before, err := h.authStore.ListWebhooks(ctx)
			require.NoError(t, err)
			res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/webhooks", map[string]any{
				"url": "https://example.com/hook", "events": []string{"*"}, "filter": filter,
			})
			require.Equal(t, http.StatusBadRequest, res.StatusCode)
			require.Equal(t, tc.code, filterRefusalCode(t, res))
			res.Body.Close()
			after, err := h.authStore.ListWebhooks(ctx)
			require.NoError(t, err)
			require.Equal(t, len(before), len(after), "a refused create stores nothing")

			id := seedWebhook(t, h)
			res = h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/webhooks/"+id.String(), map[string]any{"filter": filter})
			require.Equal(t, http.StatusBadRequest, res.StatusCode)
			require.Equal(t, tc.code, filterRefusalCode(t, res))
			res.Body.Close()
			got, err := h.authStore.GetWebhook(ctx, id)
			require.NoError(t, err)
			require.True(t, got.Filter.IsEmpty(), "a refused update changes nothing")
		})
	}
}

// 3. The audit text: an edit that only adds or changes a type or item is a
// change and must be recorded, with the ids.
func TestWebhookAudit_TypeAndItemEditsAreRecorded(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	do := h.asSession(t)
	c := h.makeCTITree(t)
	id := h.createHook(t, do, map[string]any{"url": "https://example.com/hook", "events": []string{"*"}})
	require.Len(t, h.entriesFor(t, "webhook", id), 1)

	patch := func(filter map[string]any) {
		t.Helper()
		res := do(http.MethodPatch, hooksPath+"/"+id.String(), map[string]any{"filter": filter})
		b := readAll(t, res)
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)
	}

	patch(map[string]any{"type_ids": ids(c.typ)})
	es := h.entriesFor(t, "webhook", id)
	require.Len(t, es, 2, "a types-only edit writes an entry")
	require.Equal(t, "", es[0].Before["filter"])
	require.JSONEq(t, `{"type_ids":["`+c.typ.String()+`"]}`, es[0].After["filter"].(string))

	patch(map[string]any{"type_ids": ids(c.typ), "item_ids": ids(c.item)})
	es = h.entriesFor(t, "webhook", id)
	require.Len(t, es, 3, "adding an item is an entry")
	require.JSONEq(t, `{"type_ids":["`+c.typ.String()+`"],"item_ids":["`+c.item.String()+`"]}`, es[0].After["filter"].(string))

	patch(map[string]any{})
	es = h.entriesFor(t, "webhook", id)
	require.Len(t, es, 4, "clearing it is an entry")
	require.Equal(t, "", es[0].After["filter"])
}

// 2. The redelivery state: the ticket as read for a replay carries its type and
// item, or a type filter could not be checked against it and the replay would
// fail open or closed for the wrong reason.
func TestWebhookEventSource_StateCarriesTypeAndItem(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	c := h.makeCTITree(t)
	email := "guest@test.local"
	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		Subject: "Battery swollen", Description: "bulging", CategoryID: h.catID, TypeID: &c.typ, ItemID: &c.item,
		GuestEmail: &email, GuestName: "Ada"})
	require.NoError(t, err)

	facts, found, err := server.NewWebhookEventSource(h.ticketSvc).TicketFacts(ctx, tk.ID)
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, facts.State.TypeID)
	require.NotNil(t, facts.State.ItemID)
	require.Equal(t, c.typ, *facts.State.TypeID)
	require.Equal(t, c.item, *facts.State.ItemID)

	f := notification.WebhookFilter{TypeIDs: []uuid.UUID{c.typ}, ItemIDs: []uuid.UUID{c.item}}
	admit, known := f.Admits(facts.State)
	require.True(t, admit)
	require.True(t, known)
}
