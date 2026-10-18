package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Issuing a key on someone's behalf (#450): POST /admin/api-keys with user_id.
// The key belongs to that person and acts as them; the administrator who made
// it is recorded and audited.

const adminKeys = "/api/v1/admin/api-keys"

func (h *harness) issueFor(t *testing.T, cookie string, userID uuid.UUID, scopes ...string) (*http.Response, []byte) {
	t.Helper()
	res := h.doSession(t, cookie, http.MethodPost, adminKeys, map[string]any{
		"name": "issued", "scopes": scopes, "user_id": userID.String(), "expires_at": inYears(1),
	})
	b := readAll(t, res)
	return res, b
}

func TestAdminIssueKey_ForAStaffMember(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	admin := h.session(t, asAdmin)

	res, b := h.issueFor(t, admin, h.staffID, "tickets:read")
	require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", b)
	var k struct {
		issuedKey
		UserID    uuid.UUID `json:"user_id"`
		CreatedBy uuid.UUID `json:"created_by"`
	}
	require.NoError(t, json.Unmarshal(b, &k))
	require.Equal(t, h.staffID, k.UserID)
	require.Equal(t, h.adminID, k.CreatedBy)

	t.Run("it acts as the staff member, not the administrator", func(t *testing.T) {
		r := requestWithKey(t, h, k.Token, "/api/v1/me")
		var me struct{ Role, ID string }
		require.NoError(t, json.Unmarshal(readAll(t, r), &me))
		require.Equal(t, "staff", me.Role)
		require.Equal(t, h.staffID.String(), me.ID)

		r = requestWithKey(t, h, k.Token, "/api/v1/admin/users")
		r.Body.Close()
		require.Equal(t, http.StatusForbidden, r.StatusCode)
	})

	t.Run("the list shows whose it is and who issued it", func(t *testing.T) {
		res := h.doSession(t, admin, http.MethodGet, adminKeys, nil)
		var list []struct {
			ID         uuid.UUID  `json:"id"`
			UserID     uuid.UUID  `json:"user_id"`
			OwnerEmail string     `json:"owner_email"`
			CreatedBy  *uuid.UUID `json:"created_by"`
		}
		require.NoError(t, json.Unmarshal(readAll(t, res), &list))
		found := false
		for _, e := range list {
			if e.ID == k.ID {
				found = true
				require.Equal(t, "staff@test.local", e.OwnerEmail)
				require.NotNil(t, e.CreatedBy)
				require.Equal(t, h.adminID, *e.CreatedBy)
			}
		}
		require.True(t, found)
	})

	t.Run("it is audited as issued on behalf, by the administrator", func(t *testing.T) {
		got := h.auditFor(t, k.ID)
		require.Len(t, got, 1)
		require.Equal(t, "api_key_issued", got[0]["action"])
		require.Equal(t, h.adminID, got[0]["actor"])
		after := got[0]["after"].(map[string]any)
		require.Equal(t, h.staffID.String(), after["owner_user_id"])
		require.Equal(t, true, after["on_behalf"])
	})

	t.Run("revoking it is audited too", func(t *testing.T) {
		res := h.doSession(t, admin, http.MethodDelete, adminKeys+"/"+k.ID.String(), nil)
		res.Body.Close()
		require.Equal(t, http.StatusNoContent, res.StatusCode)
		got := h.auditFor(t, k.ID)
		require.Len(t, got, 2)
		require.Equal(t, "api_key_revoked", got[0]["action"])
		require.Equal(t, h.adminID, got[0]["actor"])
		after := got[0]["after"].(map[string]any)
		require.Equal(t, h.staffID.String(), after["owner_user_id"])
		require.Equal(t, true, after["on_behalf"], "revoking someone else's key says so")

		r := requestWithKey(t, h, k.Token, "/api/v1/me")
		r.Body.Close()
		require.Equal(t, http.StatusUnauthorized, r.StatusCode)
	})
}

// A key's scopes can never exceed its owner's role, and the owner is the
// person named, not the administrator typing.
func TestAdminIssueKey_ScopesFollowTheOwnersRole(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	admin := h.session(t, asAdmin)

	cases := []struct {
		name   string
		owner  uuid.UUID
		scopes []string
		want   int
	}{
		{"staff, a staff scope", h.staffID, []string{"tickets:write", "audit:read"}, http.StatusCreated},
		{"staff, an admin-only scope", h.staffID, []string{"settings:read"}, http.StatusBadRequest},
		{"administrator, anything", h.adminID, []string{"settings:write"}, http.StatusCreated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, b := h.issueFor(t, admin, tc.owner, tc.scopes...)
			require.Equal(t, tc.want, res.StatusCode, "body: %s", b)
			if tc.want == http.StatusBadRequest {
				require.Equal(t, "invalid_scope", errCode(t, b))
			}
		})
	}
}

func TestAdminIssueKey_TheNamedUserMustBeLive(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	admin := h.session(t, asAdmin)

	res, b := h.issueFor(t, admin, uuid.New(), "tickets:read")
	require.Equal(t, http.StatusNotFound, res.StatusCode, "body: %s", b)

	r := h.doAsAdminSession(t, http.MethodPatch, "/api/v1/admin/users/"+h.staffID.String(), map[string]any{"disabled": true})
	r.Body.Close()
	res, b = h.issueFor(t, admin, h.staffID, "tickets:read")
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", b)
	require.Equal(t, "user_disabled", errCode(t, b))

	gone, err := h.userSvc.Create(context.Background(), user.CreateUserInput{
		Email: "gone@test.local", DisplayName: "Gone", Role: user.RoleStaff, Password: "password",
	})
	require.NoError(t, err)
	r = h.doAsAdminSession(t, http.MethodDelete, "/api/v1/admin/users/"+gone.ID.String(), nil)
	r.Body.Close()
	require.Equal(t, http.StatusNoContent, r.StatusCode)
	res, b = h.issueFor(t, admin, gone.ID, "tickets:read")
	require.Equal(t, http.StatusNotFound, res.StatusCode, "a deleted account is not a person to issue for: %s", b)
}

// Existing behaviour, unchanged: with no user_id the key is the caller's own,
// and an administrator's key keeps acting as an administrator.
func TestAdminIssueKey_WithoutUserIDIsTheCallersOwn(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.doSession(t, h.session(t, asAdmin), http.MethodPost, adminKeys,
		map[string]any{"name": "own", "scopes": []string{"users:read", "credentials:read"}, "expires_at": inYears(1)})
	b := readAll(t, res)
	require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", b)
	var k issuedKey
	require.NoError(t, json.Unmarshal(b, &k))

	r := requestWithKey(t, h, k.Token, "/api/v1/admin/users")
	r.Body.Close()
	require.Equal(t, http.StatusOK, r.StatusCode, "an administrator's key still acts as an administrator")

	got := h.auditFor(t, k.ID)
	require.Len(t, got, 1, "a key an administrator makes for themselves is audited too")
	require.Equal(t, false, got[0]["after"].(map[string]any)["on_behalf"])
	require.Equal(t, h.adminID.String(), got[0]["after"].(map[string]any)["owner_user_id"])
}

// A key presenting itself still cannot revoke the owner's other keys (#466);
// auditing the revoke does not change who may do it.
func TestAdminRevokeKey_AMachineCredentialStillRevokesOnlyItself(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	other := h.mintMine(t, h.session(t, asAdmin), "tickets:read")

	res := h.doAsAdmin(t, http.MethodDelete, adminKeys+"/"+other.ID.String(), nil)
	res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode)
	require.Len(t, h.auditFor(t, other.ID), 1, "a refused revoke writes nothing: only the issue is on record")
}

// The admin-wide audit view shows only allow-listed fields (#362). Without
// these in the list an administrator reading "issued a key" would see an empty
// diff, and the point of the entry, whose account it was for, would be lost.
func TestAdminAudit_ShowsWhoseKeyWasIssued(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	admin := h.session(t, asAdmin)
	res, b := h.issueFor(t, admin, h.staffID, "tickets:read")
	require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", b)
	var k issuedKey
	require.NoError(t, json.Unmarshal(b, &k))

	res = h.doSession(t, admin, http.MethodGet, "/api/v1/admin/audit?entity_type=api_key", nil)
	body := readAll(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	var page struct {
		Entries []struct {
			EntityID uuid.UUID      `json:"entity_id"`
			Action   string         `json:"action"`
			After    map[string]any `json:"after"`
		} `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(body, &page))
	require.Len(t, page.Entries, 1)
	e := page.Entries[0]
	require.Equal(t, k.ID, e.EntityID)
	require.Equal(t, "api_key_issued", e.Action)
	require.Equal(t, h.staffID.String(), e.After["owner_user_id"])
	require.Equal(t, true, e.After["on_behalf"])
	require.Equal(t, "tickets:read", e.After["scopes"])
}
