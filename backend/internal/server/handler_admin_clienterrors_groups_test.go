package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Client mistakes on the group routes answer 4xx, not 500 (#418).

func mkGroup(t *testing.T, h *harness, name string) uuid.UUID {
	t.Helper()
	g, err := h.groupSvc.Create(context.Background(), name, "")
	require.NoError(t, err)
	return g.ID
}

// clientErrorCase is one request that must be refused with a given status and
// error code. build runs after the harness exists so it can create fixtures.
type clientErrorCase struct {
	name   string
	method string
	build  func(t *testing.T, h *harness) (path string, body any)
	status int
	code   string
}

func runClientErrorCases(t *testing.T, cases []clientErrorCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			path, body := tc.build(t, h)
			res := h.doAsAdmin(t, tc.method, path, body)
			defer res.Body.Close()
			require.Equal(t, tc.status, res.StatusCode)
			require.Equal(t, tc.code, errorCode(t, res))
		})
	}
}

func TestAdminGroups_ClientErrors(t *testing.T) {
	const base = "/api/v1/admin/groups"
	runClientErrorCases(t, []clientErrorCase{
		{"get unknown", http.MethodGet, func(t *testing.T, h *harness) (string, any) {
			return base + "/" + uuid.NewString(), nil
		}, 404, "not_found"},
		{"patch unknown", http.MethodPatch, func(t *testing.T, h *harness) (string, any) {
			return base + "/" + uuid.NewString(), map[string]any{"name": "x"}
		}, 404, "not_found"},
		{"patch blank name", http.MethodPatch, func(t *testing.T, h *harness) (string, any) {
			return base + "/" + mkGroup(t, h, "A").String(), map[string]any{"name": "   "}
		}, 400, "bad_request"},
		{"patch name of another group", http.MethodPatch, func(t *testing.T, h *harness) (string, any) {
			mkGroup(t, h, "Taken")
			return base + "/" + mkGroup(t, h, "Other").String(), map[string]any{"name": "Taken"}
		}, 409, "group_name_taken"},
		{"create blank name", http.MethodPost, func(t *testing.T, h *harness) (string, any) {
			return base, map[string]any{"name": " "}
		}, 400, "bad_request"},
		{"create duplicate name", http.MethodPost, func(t *testing.T, h *harness) (string, any) {
			mkGroup(t, h, "Dup")
			return base, map[string]any{"name": "Dup"}
		}, 409, "group_name_taken"},
		{"add member unknown group", http.MethodPost, func(t *testing.T, h *harness) (string, any) {
			return base + "/" + uuid.NewString() + "/members", map[string]any{"user_id": h.staffID}
		}, 404, "not_found"},
		{"add member unknown user", http.MethodPost, func(t *testing.T, h *harness) (string, any) {
			return base + "/" + mkGroup(t, h, "G").String() + "/members", map[string]any{"user_id": uuid.New()}
		}, 400, "invalid_reference"},
		{"add member zero user", http.MethodPost, func(t *testing.T, h *harness) (string, any) {
			return base + "/" + mkGroup(t, h, "G").String() + "/members", map[string]any{"user_id": uuid.Nil}
		}, 400, "bad_request"},
		{"add member missing user", http.MethodPost, func(t *testing.T, h *harness) (string, any) {
			return base + "/" + mkGroup(t, h, "G").String() + "/members", map[string]any{}
		}, 400, "bad_request"},
		{"list members unknown group", http.MethodGet, func(t *testing.T, h *harness) (string, any) {
			return base + "/" + uuid.NewString() + "/members", nil
		}, 404, "not_found"},
		{"add scope unknown group", http.MethodPost, func(t *testing.T, h *harness) (string, any) {
			return base + "/" + uuid.NewString() + "/scopes", map[string]any{"category_id": h.catID}
		}, 404, "not_found"},
		{"add scope unknown category", http.MethodPost, func(t *testing.T, h *harness) (string, any) {
			return base + "/" + mkGroup(t, h, "G").String() + "/scopes", map[string]any{"category_id": uuid.New()}
		}, 400, "invalid_reference"},
		{"add scope unknown type", http.MethodPost, func(t *testing.T, h *harness) (string, any) {
			return base + "/" + mkGroup(t, h, "G").String() + "/scopes",
				map[string]any{"category_id": h.catID, "type_id": uuid.New()}
		}, 400, "invalid_reference"},
		{"add scope type of another category", http.MethodPost, func(t *testing.T, h *harness) (string, any) {
			other, err := h.categorySvc.CreateCategory(context.Background(), "Other", 2)
			require.NoError(t, err)
			tp, err := h.categorySvc.CreateType(context.Background(), other.ID, "T", 1)
			require.NoError(t, err)
			return base + "/" + mkGroup(t, h, "G").String() + "/scopes",
				map[string]any{"category_id": h.catID, "type_id": tp.ID}
		}, 400, "invalid_reference"},
		{"add scope zero category", http.MethodPost, func(t *testing.T, h *harness) (string, any) {
			return base + "/" + mkGroup(t, h, "G").String() + "/scopes", map[string]any{"category_id": uuid.Nil}
		}, 400, "bad_request"},
		{"add scope missing category", http.MethodPost, func(t *testing.T, h *harness) (string, any) {
			return base + "/" + mkGroup(t, h, "G").String() + "/scopes", map[string]any{}
		}, 400, "bad_request"},
		{"list scopes unknown group", http.MethodGet, func(t *testing.T, h *harness) (string, any) {
			return base + "/" + uuid.NewString() + "/scopes", nil
		}, 404, "not_found"},
	})
}

// The success paths must not move: characterisation for the same routes.
func TestAdminGroups_SuccessPathsUnchanged(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	const base = "/api/v1/admin/groups"

	res := h.doAsAdmin(t, http.MethodPost, base, map[string]any{"name": "  Ops  "})
	require.Equal(t, http.StatusCreated, res.StatusCode)
	var g struct {
		ID   uuid.UUID `json:"id"`
		Name string    `json:"name"`
	}
	decodeJSON(t, res, &g)
	require.Equal(t, "Ops", g.Name)

	// PATCH trims too (#418).
	res = h.doAsAdmin(t, http.MethodPatch, base+"/"+g.ID.String(), map[string]any{"name": "  Ops2 "})
	require.Equal(t, http.StatusOK, res.StatusCode)
	got, err := h.groupSvc.GetByID(context.Background(), g.ID)
	require.NoError(t, err)
	require.Equal(t, "Ops2", got.Name)

	res = h.doAsAdmin(t, http.MethodGet, base+"/"+g.ID.String(), nil)
	require.Equal(t, http.StatusOK, res.StatusCode)

	res = h.doAsAdmin(t, http.MethodPost, base+"/"+g.ID.String()+"/members", map[string]any{"user_id": h.staffID})
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	res = h.doAsAdmin(t, http.MethodGet, base+"/"+g.ID.String()+"/members", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)

	res = h.doAsAdmin(t, http.MethodPost, base+"/"+g.ID.String()+"/scopes", map[string]any{"category_id": h.catID})
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	res = h.doAsAdmin(t, http.MethodGet, base+"/"+g.ID.String()+"/scopes", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
}
