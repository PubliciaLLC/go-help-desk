package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// The refusals a filter can get, each with the one code it must come back
// with (#413). Every case is a 400 and stores nothing.
func badFilterCases() []struct {
	name   string
	filter any
	code   string
} {
	return []struct {
		name   string
		filter any
		code   string
	}{
		{"not an object", "critical", "invalid_filter"},
		{"unknown key (would otherwise be silently ignored and widen the hook)",
			map[string]any{"type_ids": []string{uuid.New().String()}}, "invalid_filter"},
		{"malformed uuid", map[string]any{"category_ids": []string{"nope"}}, "invalid_filter"},
		{"list is not an array", map[string]any{"priorities": "critical"}, "invalid_filter"},
		{"invert is not a boolean", map[string]any{"invert": "yes"}, "invalid_filter"},
		{"unknown priority", map[string]any{"priorities": []string{"urgent"}}, "invalid_filter_priority"},
		{"priority wrong case", map[string]any{"priorities": []string{"Critical"}}, "invalid_filter_priority"},
		{"unknown category", map[string]any{"category_ids": []uuid.UUID{uuid.New()}}, "unknown_filter_category"},
		{"unknown group", map[string]any{"group_ids": []uuid.UUID{uuid.New()}}, "unknown_filter_group"},
		{"inverted with no conditions", map[string]any{"invert": true}, "empty_inverted_filter"},
	}
}

// normalizedFilter maps nil lists to empty ones. Both mean "no condition", and
// a filter read back from the store has empty lists, so a comparison of the
// two must not depend on which one a test wrote.
func normalizedFilter(f notification.WebhookFilter) notification.WebhookFilter {
	if f.CategoryIDs == nil {
		f.CategoryIDs = []uuid.UUID{}
	}
	if f.Priorities == nil {
		f.Priorities = []string{}
	}
	if f.GroupIDs == nil {
		f.GroupIDs = []uuid.UUID{}
	}
	return f
}

// filterRefusalCode reads the code of an error response.
func filterRefusalCode(t *testing.T, res *http.Response) string {
	t.Helper()
	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
	return body.Error.Code
}

func TestWebhookCreate_StoresTheFilter(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	grp, err := h.groupSvc.Create(ctx, "Tier 2", "")
	require.NoError(t, err)

	want := notification.WebhookFilter{
		CategoryIDs: []uuid.UUID{h.catID},
		Priorities:  []string{"critical"},
		GroupIDs:    []uuid.UUID{grp.ID},
		Invert:      true,
	}
	res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/webhooks", map[string]any{
		"url": "https://example.com/hook", "events": []string{"*"}, "filter": want,
	})
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	var created struct {
		ID     string                     `json:"id"`
		Filter notification.WebhookFilter `json:"filter"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&created))
	require.Equal(t, want, created.Filter)

	stored, err := h.authStore.GetWebhook(ctx, uuid.MustParse(created.ID))
	require.NoError(t, err)
	require.Equal(t, want, stored.Filter)
}

// Absent, null and {} all mean no filter, and the answer carries the four keys
// as arrays, never null.
func TestWebhookCreate_WithoutAFilterHasAnEmptyOne(t *testing.T) {
	const empty = `{"category_ids":[],"priorities":[],"group_ids":[],"invert":false}`
	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"absent", map[string]any{"url": "https://example.com/hook", "events": []string{"*"}}},
		{"null", map[string]any{"url": "https://example.com/hook", "events": []string{"*"}, "filter": nil}},
		{"empty object", map[string]any{"url": "https://example.com/hook", "events": []string{"*"}, "filter": map[string]any{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()

			res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/webhooks", tc.body)
			defer res.Body.Close()
			require.Equal(t, http.StatusCreated, res.StatusCode)

			var created struct {
				Filter json.RawMessage `json:"filter"`
			}
			require.NoError(t, json.NewDecoder(res.Body).Decode(&created))
			require.JSONEq(t, empty, string(created.Filter))
		})
	}
}

func TestWebhookCreate_RefusesABadFilter(t *testing.T) {
	for _, tc := range badFilterCases() {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			ctx := context.Background()
			before, err := h.authStore.ListWebhooks(ctx)
			require.NoError(t, err)

			res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/webhooks", map[string]any{
				"url": "https://example.com/hook", "events": []string{"*"}, "filter": tc.filter,
			})
			defer res.Body.Close()
			require.Equal(t, http.StatusBadRequest, res.StatusCode)
			require.Equal(t, tc.code, filterRefusalCode(t, res))

			after, err := h.authStore.ListWebhooks(ctx)
			require.NoError(t, err)
			require.Equal(t, len(before), len(after), "a refused create must not store a webhook")
		})
	}
}

// The URL is checked first, so a bad URL and a bad filter report the URL.
func TestWebhookCreate_FilterIsCheckedAfterTheOtherFields(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/webhooks", map[string]any{
		"url": "not a url", "events": []string{"*"},
		"filter": map[string]any{"priorities": []string{"urgent"}},
	})
	defer res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
	require.Equal(t, "invalid_url", filterRefusalCode(t, res))
}

func TestWebhookUpdate_ReplacesTheFilter(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	id := seedWebhook(t, h)

	stored, err := h.authStore.GetWebhook(ctx, id)
	require.NoError(t, err)
	stored.Filter = notification.WebhookFilter{
		CategoryIDs: []uuid.UUID{h.catID}, Priorities: []string{"critical"},
	}
	require.NoError(t, h.authStore.UpdateWebhook(ctx, stored))

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/webhooks/"+id.String(),
		map[string]any{"filter": map[string]any{"priorities": []string{"high"}}})
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	got, err := h.authStore.GetWebhook(ctx, id)
	require.NoError(t, err)
	require.Equal(t, normalizedFilter(notification.WebhookFilter{Priorities: []string{"high"}}), normalizedFilter(got.Filter),
		"an object replaces the whole filter: the categories must be gone")
}

func TestWebhookUpdate_OmittingTheFilterKeepsIt(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	id := seedWebhook(t, h)

	want := notification.WebhookFilter{Priorities: []string{"critical"}, Invert: true}
	stored, err := h.authStore.GetWebhook(ctx, id)
	require.NoError(t, err)
	stored.Filter = want
	require.NoError(t, h.authStore.UpdateWebhook(ctx, stored))

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/webhooks/"+id.String(),
		map[string]any{"enabled": false})
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	got, err := h.authStore.GetWebhook(ctx, id)
	require.NoError(t, err)
	require.Equal(t, normalizedFilter(want), normalizedFilter(got.Filter))
}

func TestWebhookUpdate_NullFilterClearsIt(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	id := seedWebhook(t, h)

	stored, err := h.authStore.GetWebhook(ctx, id)
	require.NoError(t, err)
	stored.Filter = notification.WebhookFilter{Priorities: []string{"critical"}}
	require.NoError(t, h.authStore.UpdateWebhook(ctx, stored))

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/webhooks/"+id.String(),
		map[string]any{"filter": nil})
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	got, err := h.authStore.GetWebhook(ctx, id)
	require.NoError(t, err)
	require.True(t, got.Filter.IsEmpty(), "a null filter must clear the stored one")
}

func TestWebhookUpdate_RefusesABadFilter(t *testing.T) {
	for _, tc := range badFilterCases() {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			ctx := context.Background()
			id := seedWebhook(t, h)
			before, err := h.authStore.GetWebhook(ctx, id)
			require.NoError(t, err)

			res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/webhooks/"+id.String(),
				map[string]any{"url": "https://example.com/other", "filter": tc.filter})
			defer res.Body.Close()
			require.Equal(t, http.StatusBadRequest, res.StatusCode)
			require.Equal(t, tc.code, filterRefusalCode(t, res))

			after, err := h.authStore.GetWebhook(ctx, id)
			require.NoError(t, err)
			require.Equal(t, before, after, "a refused update must not half-apply")
		})
	}
}

// A stored filter can name a group that is later deleted. The filter is
// checked only when it is sent, so an unrelated PATCH still succeeds.
func TestWebhookUpdate_OtherFieldsAreNotGatedOnTheFilter(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	id := seedWebhook(t, h)
	grp, err := h.groupSvc.Create(ctx, "Tier 2", "")
	require.NoError(t, err)

	stored, err := h.authStore.GetWebhook(ctx, id)
	require.NoError(t, err)
	stored.Filter = notification.WebhookFilter{GroupIDs: []uuid.UUID{grp.ID}}
	require.NoError(t, h.authStore.UpdateWebhook(ctx, stored))
	_, err = h.ticketSvc.DeleteGroup(ctx, nil, grp.ID)
	require.NoError(t, err)

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/webhooks/"+id.String(),
		map[string]any{"enabled": false})
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestWebhookList_ShowsTheFilter(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	id := seedWebhook(t, h)

	want := notification.WebhookFilter{CategoryIDs: []uuid.UUID{h.catID}, Invert: true}
	stored, err := h.authStore.GetWebhook(ctx, id)
	require.NoError(t, err)
	stored.Filter = want
	require.NoError(t, h.authStore.UpdateWebhook(ctx, stored))

	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/webhooks", nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	var list []struct {
		ID     string                     `json:"id"`
		Filter notification.WebhookFilter `json:"filter"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&list))
	var found bool
	for _, w := range list {
		if w.ID == id.String() {
			found = true
			require.Equal(t, normalizedFilter(want), normalizedFilter(w.Filter))
		}
	}
	require.True(t, found, "the seeded webhook is not in the list")
}

// An inactive category still has tickets, so a filter may name it (#413).
func TestWebhookCreate_AcceptsAnInactiveCategory(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	cat, err := h.categorySvc.CreateCategory(ctx, "Legacy", 99)
	require.NoError(t, err)
	cat.Active = false
	require.NoError(t, h.categorySvc.UpdateCategory(ctx, cat))

	res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/webhooks", map[string]any{
		"url": "https://example.com/hook", "events": []string{"*"},
		"filter": map[string]any{"category_ids": []uuid.UUID{cat.ID}},
	})
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)
}

// A category or group that existed and was deleted is unknown: a filter still
// naming it is refused on save, so it has to be unticked first.
func TestWebhookUpdate_RefusesAFilterNamingADeletedCategoryOrGroup(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	id := seedWebhook(t, h)

	cat, err := h.categorySvc.CreateCategory(ctx, "Gone", 99)
	require.NoError(t, err)
	require.NoError(t, h.categorySvc.DeleteCategory(ctx, cat.ID))
	grp, err := h.groupSvc.Create(ctx, "Gone", "")
	require.NoError(t, err)
	_, err = h.ticketSvc.DeleteGroup(ctx, nil, grp.ID)
	require.NoError(t, err)

	for _, tc := range []struct {
		filter map[string]any
		code   string
	}{
		{map[string]any{"category_ids": []uuid.UUID{cat.ID}}, "unknown_filter_category"},
		{map[string]any{"group_ids": []uuid.UUID{grp.ID}}, "unknown_filter_group"},
	} {
		res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/webhooks/"+id.String(),
			map[string]any{"filter": tc.filter})
		require.Equal(t, http.StatusBadRequest, res.StatusCode)
		require.Equal(t, tc.code, filterRefusalCode(t, res))
		res.Body.Close()
	}
}

// Within a filter the first failure wins, in the documented order: shape,
// priority, category, group. Each case breaks two checks and expects the
// earlier one.
func TestWebhookFilter_RefusalOrder(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	unknown := []uuid.UUID{uuid.New()}

	for _, tc := range []struct {
		name   string
		filter map[string]any
		code   string
	}{
		{"shape before priority", map[string]any{"priorities": []string{"urgent"}, "type_ids": unknown}, "invalid_filter"},
		{"priority before category", map[string]any{"priorities": []string{"urgent"}, "category_ids": unknown}, "invalid_filter_priority"},
		{"category before group", map[string]any{"category_ids": unknown, "group_ids": unknown}, "unknown_filter_category"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/webhooks", map[string]any{
				"url": "https://example.com/hook", "events": []string{"*"}, "filter": tc.filter,
			})
			defer res.Body.Close()
			require.Equal(t, http.StatusBadRequest, res.StatusCode)
			require.Equal(t, tc.code, filterRefusalCode(t, res))
		})
	}
}

// On update too the filter is checked after the fields before it: a bad URL
// and a bad filter report the URL.
func TestWebhookUpdate_FilterIsCheckedAfterTheOtherFields(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := seedWebhook(t, h)

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/webhooks/"+id.String(), map[string]any{
		"url": "not a url", "filter": map[string]any{"priorities": []string{"urgent"}},
	})
	defer res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
	require.Equal(t, "invalid_url", filterRefusalCode(t, res))
}
