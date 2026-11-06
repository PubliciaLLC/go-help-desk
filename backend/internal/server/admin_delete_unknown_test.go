package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// #528: an admin DELETE of an id that does not exist answers 404, as the rest
// of the admin DELETEs do, instead of a 204 that claims something was removed.

func TestAdminDeleteCannedResponse_UnknownID(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodDelete, "/api/v1/admin/canned-responses/"+uuid.New().String(), nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestAdminDeleteTag_UnknownID(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodDelete, "/api/v1/admin/tags/"+uuid.New().String(), nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// Deleting a tag twice is not an unknown id: the second call finds the row,
// which is already deleted, and says so the way the first did.
func TestAdminDeleteTag_AlreadyDeletedStaysNoContent(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tg, err := h.q.CreateTag(context.Background(), "once-twice")
	require.NoError(t, err)

	for i := 0; i < 2; i++ {
		resp := h.doAsAdmin(t, http.MethodDelete, "/api/v1/admin/tags/"+tg.ID.String(), nil)
		require.Equal(t, http.StatusNoContent, resp.StatusCode, "delete #%d", i+1)
	}
}

func TestAdminRestoreTag_UnknownID(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/tags/"+uuid.New().String()+"/restore", nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// A canned response scoped to a category or type that is not there trips a
// foreign key. That is the caller's value, not a server fault: 400.
//
// Each case gets its own harness: a foreign-key violation aborts the harness
// transaction, so nothing after it in the same transaction can run.
func TestAdminCannedResponse_UnknownScopeIs400(t *testing.T) {
	cases := []struct {
		name  string
		scope func(h *harness, otherType uuid.UUID) map[string]any
	}{
		{"unknown category", func(h *harness, _ uuid.UUID) map[string]any {
			return map[string]any{"category_id": uuid.New().String()}
		}},
		{"unknown type", func(h *harness, _ uuid.UUID) map[string]any {
			return map[string]any{"category_id": h.catID.String(), "type_id": uuid.New().String()}
		}},
		// Both ids exist, but the type belongs to another category.
		{"type from another category", func(h *harness, otherType uuid.UUID) map[string]any {
			return map[string]any{"category_id": h.catID.String(), "type_id": otherType.String()}
		}},
	}
	for _, tc := range cases {
		for _, verb := range []string{"create", "update"} {
			t.Run(verb+": "+tc.name, func(t *testing.T) {
				h, cleanup := newHarness(t)
				defer cleanup()
				ctx := context.Background()

				otherCat, err := h.categorySvc.CreateCategory(ctx, "Other", 2)
				require.NoError(t, err)
				otherType, err := h.categorySvc.CreateType(ctx, otherCat.ID, "Other type", 1)
				require.NoError(t, err)

				body := map[string]any{"name": "Scoped", "body": "Body"}
				for k, v := range tc.scope(h, otherType.ID) {
					body[k] = v
				}
				var resp *http.Response
				if verb == "create" {
					resp = h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/canned-responses", body)
				} else {
					cr, err := h.cannedResponses.Create(ctx, "To scope", "Body", nil, nil, 0)
					require.NoError(t, err)
					resp = h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/canned-responses/"+cr.ID.String(), body)
				}
				require.Equal(t, http.StatusBadRequest, resp.StatusCode)
			})
		}
	}
}
