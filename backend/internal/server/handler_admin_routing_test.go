package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// #447: auto_assign_group_id and auto_assign_user_ids used to accept anything
// and ignore what they could not use, so a typo saved cleanly and routing
// quietly did nothing. Both are checked at save time now.

func patchSettings(t *testing.T, h *harness, body map[string]any) (int, string) {
	t.Helper()
	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings", body)
	defer res.Body.Close()
	return res.StatusCode, readBody(t, res)
}

func TestAutoAssignGroup_SaveIsChecked(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	g, err := h.groupSvc.Create(ctx, "Triage", "")
	require.NoError(t, err)

	t.Run("an existing group is saved", func(t *testing.T) {
		status, body := patchSettings(t, h, map[string]any{"auto_assign_group_id": g.ID.String()})
		require.Equal(t, http.StatusNoContent, status, body)
		got := h.adminSvc.AutoAssignGroupID(ctx)
		require.NotNil(t, got)
		require.Equal(t, g.ID, *got)
	})

	t.Run("empty or null clears it", func(t *testing.T) {
		for _, v := range []any{"", nil} {
			status, body := patchSettings(t, h, map[string]any{"auto_assign_group_id": g.ID.String()})
			require.Equal(t, http.StatusNoContent, status, body)
			status, body = patchSettings(t, h, map[string]any{"auto_assign_group_id": v})
			require.Equal(t, http.StatusNoContent, status, body)
			require.Nil(t, h.adminSvc.AutoAssignGroupID(ctx))
		}
	})

	for name, v := range map[string]any{
		"unknown id":    uuid.NewString(),
		"not a uuid":    "triage",
		"wrong type":    42,
		"a list":        []string{g.ID.String()},
		"the zero uuid": uuid.Nil.String(),
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			// Start from a known good value to show a refusal changes nothing.
			status, _ := patchSettings(t, h, map[string]any{"auto_assign_group_id": g.ID.String()})
			require.Equal(t, http.StatusNoContent, status)

			status, body := patchSettings(t, h, map[string]any{"auto_assign_group_id": v})
			require.Equal(t, http.StatusBadRequest, status, body)
			got := h.adminSvc.AutoAssignGroupID(ctx)
			require.NotNil(t, got)
			require.Equal(t, g.ID, *got, "a refused save must not write")
		})
	}
}

func TestAutoAssignUsers_SaveIsChecked(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	staff, admin := h.staffID.String(), h.adminID.String()
	gone, err := h.userSvc.Create(ctx, userInput("gone@test.local", "Gone"))
	require.NoError(t, err)
	require.NoError(t, h.userSvc.SoftDelete(ctx, gone.ID))

	t.Run("staff and admin accounts are saved, in order", func(t *testing.T) {
		status, body := patchSettings(t, h, map[string]any{"auto_assign_user_ids": []string{staff, admin}})
		require.Equal(t, http.StatusNoContent, status, body)
		require.Equal(t, []uuid.UUID{h.staffID, h.adminID}, h.adminSvc.AutoAssignUserIDs(ctx))
	})

	t.Run("empty list or null clears it", func(t *testing.T) {
		for _, v := range []any{[]string{}, nil} {
			status, _ := patchSettings(t, h, map[string]any{"auto_assign_user_ids": []string{staff}})
			require.Equal(t, http.StatusNoContent, status)
			status, body := patchSettings(t, h, map[string]any{"auto_assign_user_ids": v})
			require.Equal(t, http.StatusNoContent, status, body)
			require.Empty(t, h.adminSvc.AutoAssignUserIDs(ctx))
		}
	})

	for name, v := range map[string]any{
		"an unknown id":               []string{staff, uuid.NewString()},
		"not a uuid":                  []string{staff, "bob"},
		"a reporting user, not staff": []string{staff, h.userID.String()},
		"a string instead of a list":  staff,
		"a number in the list":        []any{staff, 7},
		"a deleted account":           []string{staff, gone.ID.String()},
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			status, _ := patchSettings(t, h, map[string]any{"auto_assign_user_ids": []string{admin}})
			require.Equal(t, http.StatusNoContent, status)

			status, body := patchSettings(t, h, map[string]any{"auto_assign_user_ids": v})
			require.Equal(t, http.StatusBadRequest, status, body)
			require.Equal(t, []uuid.UUID{h.adminID}, h.adminSvc.AutoAssignUserIDs(ctx), "a refused save must not write")
		})
	}
}

// A body with one good and one bad routing setting writes neither.
func TestAutoAssign_RefusalWritesNothing(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	g, err := h.groupSvc.Create(ctx, "Triage", "")
	require.NoError(t, err)

	status, body := patchSettings(t, h, map[string]any{
		"auto_assign_group_id": g.ID.String(),
		"auto_assign_user_ids": []string{uuid.NewString()},
	})
	require.Equal(t, http.StatusBadRequest, status, body)
	require.Nil(t, h.adminSvc.AutoAssignGroupID(ctx))
}
