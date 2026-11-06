package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Keys can't write users (CTO, 2026-10-16). An API key or OAuth client may only
// READ users. Every user write, and every group membership or scope change
// (which grants ticket visibility to users), needs a signed-in administrator.
//
// The per-field rules this replaces (no password on create, no reset_mfa, no
// email, nothing about administrators) each closed one door of the same room.
// The round-6 review found another: a key promotes a requester to staff, the
// requester's own password signs in, and that staff session mints a key.

type userWriteRoute struct {
	name, method string
	path         func(h *harness, g uuid.UUID) string
	body         func(h *harness) any
}

func userWriteRoutes() []userWriteRoute {
	user := func(h *harness) string { return usersPath + "/" + h.staffID.String() }
	return []userWriteRoute{
		{"create a user", http.MethodPost,
			func(h *harness, _ uuid.UUID) string { return usersPath },
			func(h *harness) any {
				return map[string]any{"email": "made@test.local", "display_name": "Made", "role": "staff"}
			}},
		{"rename a user", http.MethodPatch,
			func(h *harness, _ uuid.UUID) string { return user(h) },
			func(h *harness) any { return map[string]any{"display_name": "Renamed"} }},
		{"promote a requester", http.MethodPatch,
			func(h *harness, _ uuid.UUID) string { return usersPath + "/" + h.userID.String() },
			func(h *harness) any { return map[string]any{"role": "staff"} }},
		{"disable a user", http.MethodPatch,
			func(h *harness, _ uuid.UUID) string { return user(h) },
			func(h *harness) any { return map[string]any{"disabled": true} }},
		{"delete a user", http.MethodDelete,
			func(h *harness, _ uuid.UUID) string { return user(h) },
			func(h *harness) any { return nil }},
		{"set a password", http.MethodPost,
			func(h *harness, _ uuid.UUID) string { return user(h) + "/password" },
			func(h *harness) any { return map[string]any{"new_password": "a-password-the-key-chose"} }},
		{"add a group member", http.MethodPost,
			func(h *harness, g uuid.UUID) string { return fmt.Sprintf("/api/v1/admin/groups/%s/members", g) },
			func(h *harness) any { return map[string]any{"user_id": h.staffID.String()} }},
		{"remove a group member", http.MethodDelete,
			func(h *harness, g uuid.UUID) string {
				return fmt.Sprintf("/api/v1/admin/groups/%s/members/%s", g, h.userID)
			},
			func(h *harness) any { return nil }},
		{"add a group scope", http.MethodPost,
			func(h *harness, g uuid.UUID) string { return fmt.Sprintf("/api/v1/admin/groups/%s/scopes", g) },
			func(h *harness) any { return map[string]any{"category_id": h.catID.String()} }},
		{"remove a group scope", http.MethodDelete,
			func(h *harness, g uuid.UUID) string { return fmt.Sprintf("/api/v1/admin/groups/%s/scopes", g) },
			func(h *harness) any { return map[string]any{"category_id": h.catID.String()} }},
	}
}

// world is everything a refused write could have changed.
type world struct {
	Users   map[uuid.UUID]string
	Members []uuid.UUID
	Scopes  int
}

func (h *harness) snapshot(t *testing.T, g uuid.UUID) world {
	t.Helper()
	ctx := context.Background()
	w := world{Users: map[uuid.UUID]string{}}
	for _, id := range []uuid.UUID{h.adminID, h.staffID, h.userID} {
		u, err := h.userSvc.GetByIDAdmin(ctx, id)
		require.NoError(t, err)
		w.Users[id] = fmt.Sprintf("%s|%s|%s|%v|%v|%s", u.Email, u.DisplayName, u.Role, u.Disabled, u.DeletedAt != nil, u.PasswordHash)
	}
	m, err := h.groupSvc.ListMembers(ctx, g)
	require.NoError(t, err)
	w.Members = m
	sc, err := h.groupSvc.ListScopes(ctx, g)
	require.NoError(t, err)
	w.Scopes = len(sc)
	return w
}

// A group with a member and a scope, so that removing either has something to
// remove and a refused removal is visible.
func (h *harness) groupWithMemberAndScope(t *testing.T) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	g, err := h.groupSvc.Create(ctx, "Desk", "")
	require.NoError(t, err)
	require.NoError(t, h.groupSvc.AddMember(ctx, g.ID, h.userID))
	res := h.doAsAdminSession(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/groups/%s/scopes", g.ID),
		map[string]any{"category_id": h.catID.String()})
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	return g.ID
}

func TestKeysCannotWriteUsers_EveryRouteRefusesAKey(t *testing.T) {
	for _, rt := range userWriteRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			g := h.groupWithMemberAndScope(t)
			client := h.newServiceClient(t, "writer", allScopes()...)
			before := h.snapshot(t, g)

			// An administrator's API key, holding every scope.
			res := h.doAsAdmin(t, rt.method, rt.path(h, g), rt.body(h))
			b := readAll(t, res)
			require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", b)
			require.Equal(t, "session_required", errCode(t, b))

			// An OAuth client is a staff identity: the admin role check refuses it.
			res = h.doBearer(t, client.Token, rt.method, rt.path(h, g), rt.body(h))
			b = readAll(t, res)
			require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", b)

			require.Equal(t, before, h.snapshot(t, g), "a refused write changed something")
		})
	}
}

func TestKeysCannotWriteUsers_ASessionCanDoAllOfIt(t *testing.T) {
	for _, rt := range userWriteRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			g := h.groupWithMemberAndScope(t)

			res := h.doAsAdminSession(t, rt.method, rt.path(h, g), rt.body(h))
			b := readAll(t, res)
			require.Less(t, res.StatusCode, 300, "body: %s", b)
		})
	}
}

func TestKeysCannotWriteUsers_KeysCanStillRead(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	g := h.groupWithMemberAndScope(t)
	for _, path := range []string{
		usersPath,
		usersPath + "/" + h.staffID.String(),
		fmt.Sprintf("/api/v1/admin/groups/%s/members", g),
		fmt.Sprintf("/api/v1/admin/groups/%s/scopes", g),
		"/api/v1/staff",
	} {
		res := h.doAsAdmin(t, http.MethodGet, path, nil)
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode, path)
	}
}

// The round-6 chain: promote a requester, sign in with their own password, mint
// a key from the staff session. It must fail at step 1.
func TestKeysCannotWriteUsers_PromoteThenMintFailsAtStepOne(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.doAsAdmin(t, http.MethodPatch, usersPath+"/"+h.userID.String(), map[string]any{"role": "staff"})
	b := readAll(t, res)
	require.Equal(t, http.StatusForbidden, res.StatusCode, "step 1 must fail: %s", b)

	// Step 2 still signs in, as the requester it still is.
	s := &session{h: h}
	login, lb := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "user@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, login.StatusCode, string(lb))
	var who struct{ Role string }
	_, me := s.send(t, http.MethodGet, "/api/v1/me", nil)
	require.NoError(t, json.Unmarshal(me, &who))
	require.Equal(t, "user", who.Role)

	// Step 3: a requester cannot mint a key.
	h.allowStaffKeys(t)
	res2, out := s.send(t, http.MethodPost, meKeys,
		map[string]any{"name": "x", "scopes": []string{"tickets:read"}, "expires_at": inYears(1)})
	require.Equal(t, http.StatusForbidden, res2.StatusCode, string(out))
}
