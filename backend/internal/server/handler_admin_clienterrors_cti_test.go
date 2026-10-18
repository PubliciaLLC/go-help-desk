package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// Client mistakes on the category/type/item routes answer 4xx, not 500 (#418).

const ctiBase = "/api/v1/admin/categories"

// mkCTI creates a fresh category > type > item chain.
func mkCTI(t *testing.T, h *harness) (category.Category, category.Type, category.Item) {
	t.Helper()
	ctx := context.Background()
	c, err := h.categorySvc.CreateCategory(ctx, "Cat-"+uuid.NewString()[:8], 1)
	require.NoError(t, err)
	tp, err := h.categorySvc.CreateType(ctx, c.ID, "Type", 1)
	require.NoError(t, err)
	it, err := h.categorySvc.CreateItem(ctx, tp.ID, "Item", 1)
	require.NoError(t, err)
	return c, tp, it
}

// ticketAt files a ticket at the given place in the CTI tree.
func ticketAt(t *testing.T, h *harness, c category.Category, tp *category.Type, it *category.Item) {
	t.Helper()
	in := ticket.CreateInput{
		Subject: "t", Description: "x", CategoryID: c.ID,
		ReporterUserID: &h.staffID,
	}
	if tp != nil {
		in.TypeID = &tp.ID
	}
	if it != nil {
		in.ItemID = &it.ID
	}
	_, err := h.ticketSvc.Create(context.Background(), in)
	require.NoError(t, err)
}

func TestAdminCTI_ClientErrors(t *testing.T) {
	runClientErrorCases(t, []clientErrorCase{
		{"get category unknown", http.MethodGet, func(t *testing.T, h *harness) (string, any) {
			return ctiBase + "/" + uuid.NewString(), nil
		}, 404, "not_found"},
		{"patch category unknown", http.MethodPatch, func(t *testing.T, h *harness) (string, any) {
			return ctiBase + "/" + uuid.NewString(), map[string]any{"name": "x"}
		}, 404, "not_found"},
		{"patch type unknown", http.MethodPatch, func(t *testing.T, h *harness) (string, any) {
			c, _, _ := mkCTI(t, h)
			return ctiBase + "/" + c.ID.String() + "/types/" + uuid.NewString(), map[string]any{"name": "x"}
		}, 404, "not_found"},
		{"patch item unknown", http.MethodPatch, func(t *testing.T, h *harness) (string, any) {
			c, tp, _ := mkCTI(t, h)
			return ctiBase + "/" + c.ID.String() + "/types/" + tp.ID.String() + "/items/" + uuid.NewString(),
				map[string]any{"name": "x"}
		}, 404, "not_found"},
		{"patch category blank name", http.MethodPatch, func(t *testing.T, h *harness) (string, any) {
			c, _, _ := mkCTI(t, h)
			return ctiBase + "/" + c.ID.String(), map[string]any{"name": "  "}
		}, 400, "bad_request"},
		{"patch type blank name", http.MethodPatch, func(t *testing.T, h *harness) (string, any) {
			c, tp, _ := mkCTI(t, h)
			return ctiBase + "/" + c.ID.String() + "/types/" + tp.ID.String(), map[string]any{"name": "  "}
		}, 400, "bad_request"},
		{"patch item blank name", http.MethodPatch, func(t *testing.T, h *harness) (string, any) {
			c, tp, it := mkCTI(t, h)
			return ctiBase + "/" + c.ID.String() + "/types/" + tp.ID.String() + "/items/" + it.ID.String(),
				map[string]any{"name": "  "}
		}, 400, "bad_request"},
		{"create category blank name", http.MethodPost, func(t *testing.T, h *harness) (string, any) {
			return ctiBase, map[string]any{"name": " "}
		}, 400, "bad_request"},
		{"create type blank name", http.MethodPost, func(t *testing.T, h *harness) (string, any) {
			c, _, _ := mkCTI(t, h)
			return ctiBase + "/" + c.ID.String() + "/types", map[string]any{"name": " "}
		}, 400, "bad_request"},
		{"create item blank name", http.MethodPost, func(t *testing.T, h *harness) (string, any) {
			c, tp, _ := mkCTI(t, h)
			return ctiBase + "/" + c.ID.String() + "/types/" + tp.ID.String() + "/items", map[string]any{"name": " "}
		}, 400, "bad_request"},
		{"create type under missing category", http.MethodPost, func(t *testing.T, h *harness) (string, any) {
			return ctiBase + "/" + uuid.NewString() + "/types", map[string]any{"name": "T"}
		}, 404, "not_found"},
		{"create item under missing type", http.MethodPost, func(t *testing.T, h *harness) (string, any) {
			c, _, _ := mkCTI(t, h)
			return ctiBase + "/" + c.ID.String() + "/types/" + uuid.NewString() + "/items", map[string]any{"name": "I"}
		}, 404, "not_found"},
		{"delete category with a ticket", http.MethodDelete, func(t *testing.T, h *harness) (string, any) {
			c, _, _ := mkCTI(t, h)
			ticketAt(t, h, c, nil, nil)
			return ctiBase + "/" + c.ID.String(), nil
		}, 409, "category_in_use"},
		{"delete type with a ticket", http.MethodDelete, func(t *testing.T, h *harness) (string, any) {
			c, tp, _ := mkCTI(t, h)
			ticketAt(t, h, c, &tp, nil)
			return ctiBase + "/" + c.ID.String() + "/types/" + tp.ID.String(), nil
		}, 409, "type_in_use"},
		{"delete type whose item has a ticket", http.MethodDelete, func(t *testing.T, h *harness) (string, any) {
			c, tp, it := mkCTI(t, h)
			ticketAt(t, h, c, &tp, &it)
			return ctiBase + "/" + c.ID.String() + "/types/" + tp.ID.String(), nil
		}, 409, "type_in_use"},
		{"delete item with a ticket", http.MethodDelete, func(t *testing.T, h *harness) (string, any) {
			c, tp, it := mkCTI(t, h)
			ticketAt(t, h, c, &tp, &it)
			return ctiBase + "/" + c.ID.String() + "/types/" + tp.ID.String() + "/items/" + it.ID.String(), nil
		}, 409, "item_in_use"},
	})
}

// The refusal names how many tickets depend on the row, like policy_in_use.
func TestAdminCTI_InUseMessageCarriesTheCount(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c, _, _ := mkCTI(t, h)
	ticketAt(t, h, c, nil, nil)
	ticketAt(t, h, c, nil, nil)

	res := h.doAsAdmin(t, http.MethodDelete, ctiBase+"/"+c.ID.String(), nil)
	require.Equal(t, http.StatusConflict, res.StatusCode)
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeJSON(t, res, &body)
	require.Contains(t, body.Error.Message, "2 ticket(s)")
}

// The category delete cascades to its SLA policies, and sla_records.policy_id
// is RESTRICT. Even after every ticket has left the category, a record still
// pointing at one of its policies makes the delete fail -- the 500 the issue
// describes -- so it must answer 409 category_in_use.
func TestAdminCTI_DeleteCategory_BlockedBySLARecord(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeySLAEnabled, true))

	c, _, _ := mkCTI(t, h)
	elsewhere, _, _ := mkCTI(t, h)
	_, err := h.slaSvc.CreatePolicy(ctx, sla.Policy{
		Name: "Per-category", CategoryID: &c.ID, ResponseTargetMin: 60, ResolutionTargetMin: 480,
	})
	require.NoError(t, err)
	ticketAt(t, h, c, nil, nil)

	// Move the ticket out of the category so only the SLA record is left.
	_, err = h.tx.ExecContext(ctx, `UPDATE tickets SET category_id = $1, type_id = NULL, item_id = NULL WHERE category_id = $2`,
		elsewhere.ID, c.ID)
	require.NoError(t, err)

	res := h.doAsAdmin(t, http.MethodDelete, ctiBase+"/"+c.ID.String(), nil)
	require.Equal(t, http.StatusConflict, res.StatusCode)
	require.Equal(t, "category_in_use", errorCode(t, res))
}

// Success paths must not move.
func TestAdminCTI_SuccessPathsUnchanged(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c, tp, it := mkCTI(t, h)
	cid, tid, iid := c.ID.String(), tp.ID.String(), it.ID.String()

	for _, tc := range []struct {
		method, path string
		body         any
		status       int
	}{
		{http.MethodGet, ctiBase + "/" + cid, nil, 200},
		{http.MethodPatch, ctiBase + "/" + cid, map[string]any{"name": "Renamed"}, 200},
		{http.MethodPatch, ctiBase + "/" + cid + "/types/" + tid, map[string]any{"name": "Renamed"}, 200},
		{http.MethodPatch, ctiBase + "/" + cid + "/types/" + tid + "/items/" + iid, map[string]any{"name": "Renamed"}, 200},
		{http.MethodPost, ctiBase, map[string]any{"name": "New"}, 201},
		{http.MethodPost, ctiBase + "/" + cid + "/types", map[string]any{"name": "New"}, 201},
		{http.MethodPost, ctiBase + "/" + cid + "/types/" + tid + "/items", map[string]any{"name": "New"}, 201},
		// Unused rows delete, children first.
		{http.MethodDelete, ctiBase + "/" + cid + "/types/" + tid + "/items/" + iid, nil, 204},
		{http.MethodDelete, ctiBase + "/" + cid + "/types/" + tid, nil, 204},
		{http.MethodDelete, ctiBase + "/" + cid, nil, 204},
	} {
		res := h.doAsAdmin(t, tc.method, tc.path, tc.body)
		require.Equal(t, tc.status, res.StatusCode, "%s %s", tc.method, tc.path)
		res.Body.Close()
	}
}
