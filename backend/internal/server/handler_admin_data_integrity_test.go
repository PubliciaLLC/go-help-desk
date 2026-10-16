package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// #419, data-integrity half: admin changes that leave data behind or half
// applied.

const adminAPI = "/api/v1/admin"

func assignmentCount(t *testing.T, h *harness, scope customfield.ScopeType, id uuid.UUID) int {
	t.Helper()
	got, err := h.customFieldSvc.ListAssignments(context.Background(), scope, id)
	require.NoError(t, err)
	return len(got)
}

// ── Custom-field assignments go with the node ────────────────────────────────

// custom_field_assignments.scope_id has no foreign key, so deleting a
// category, type or item left its assignments pointing at nothing, and GET on
// that scope still listed them.
func TestDeleteCTINode_TakesItsFieldAssignmentsWithIt(t *testing.T) {
	cases := []struct {
		name string
		path func(c, tp, it uuid.UUID) string
		// gone and kept are the scopes whose assignments must disappear and
		// survive.
		gone []customfield.ScopeType
		kept []customfield.ScopeType
	}{
		{"item",
			func(c, tp, it uuid.UUID) string {
				return adminAPI + "/categories/" + c.String() + "/types/" + tp.String() + "/items/" + it.String()
			},
			[]customfield.ScopeType{customfield.ItemScope},
			[]customfield.ScopeType{customfield.CategoryScope, customfield.TypeScope}},
		{"type, and the items under it",
			func(c, tp, it uuid.UUID) string {
				return adminAPI + "/categories/" + c.String() + "/types/" + tp.String()
			},
			[]customfield.ScopeType{customfield.TypeScope, customfield.ItemScope},
			[]customfield.ScopeType{customfield.CategoryScope}},
		{"category, and everything under it",
			func(c, tp, it uuid.UUID) string { return adminAPI + "/categories/" + c.String() },
			[]customfield.ScopeType{customfield.CategoryScope, customfield.TypeScope, customfield.ItemScope},
			nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			c, tp, it := mkCTI(t, h)
			ids := map[customfield.ScopeType]uuid.UUID{
				customfield.CategoryScope: c.ID, customfield.TypeScope: tp.ID, customfield.ItemScope: it.ID,
			}
			for scope, id := range ids {
				mkAssignment(t, h, scope, id)
			}

			res := h.doAsAdmin(t, http.MethodDelete, tc.path(c.ID, tp.ID, it.ID), nil)
			res.Body.Close()
			require.Equal(t, http.StatusNoContent, res.StatusCode)

			for _, scope := range tc.gone {
				require.Zero(t, assignmentCount(t, h, scope, ids[scope]), "%s assignments left behind", scope)
			}
			for _, scope := range tc.kept {
				require.Equal(t, 1, assignmentCount(t, h, scope, ids[scope]), "%s assignments must not be touched", scope)
			}
		})
	}
}

// A refused delete leaves the assignments alone: they go with the node or not
// at all.
func TestDeleteCategory_RefusedKeepsItsFieldAssignments(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c, tp, _ := mkCTI(t, h)
	mkAssignment(t, h, customfield.CategoryScope, c.ID)
	ticketAt(t, h, c, &tp, nil) // a ticket makes the category undeletable

	res := h.doAsAdmin(t, http.MethodDelete, adminAPI+"/categories/"+c.ID.String(), nil)
	res.Body.Close()
	require.Equal(t, http.StatusConflict, res.StatusCode)
	require.Equal(t, 1, assignmentCount(t, h, customfield.CategoryScope, c.ID))
}

// ── Deleting a group ─────────────────────────────────────────────────────────

// The database cleared assignee_group_id by itself, with nothing in the trail.
func TestDeleteGroup_UnassignsItsTicketsWithAnAuditEntryEach(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	gid := mkGroup(t, h, "Doomed")
	other := mkGroup(t, h, "Survivor")

	mk := func(subject string, g uuid.UUID) ticket.Ticket {
		tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
			Subject: subject, Description: "x", CategoryID: h.catID, ReporterUserID: &h.staffID,
		})
		require.NoError(t, err)
		_, err = h.ticketSvc.Assign(ctx, tk.ID, nil, &g, ticket.SystemActor)
		require.NoError(t, err)
		return tk
	}
	open := mk("open", gid)
	closed := mk("resolved", gid)
	res := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets/"+closed.ID.String()+"/resolve", map[string]any{})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	elsewhere := mk("someone else's", other)

	res = h.doAsAdmin(t, http.MethodDelete, adminAPI+"/groups/"+gid.String(), nil)
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	_, err := h.groupSvc.GetByID(ctx, gid)
	require.ErrorIs(t, err, group.ErrNotFound)

	unassigned := func(id uuid.UUID) []string {
		entries, err := h.auditStore.ListByEntity(ctx, "ticket", id, 50, 0)
		require.NoError(t, err)
		var actors []string
		for _, e := range entries {
			if e.Action == "unassigned" {
				require.NotNil(t, e.ActorID)
				actors = append(actors, e.ActorID.String())
				require.Equal(t, gid.String(), e.Before["assignee_group_id"])
			}
		}
		return actors
	}
	for _, tk := range []ticket.Ticket{open, closed} {
		got, err := h.ticketSvc.GetByID(ctx, tk.ID)
		require.NoError(t, err)
		require.Nil(t, got.AssigneeGroupID, "%s still names the deleted group", tk.Subject)
		require.Equal(t, []string{h.adminID.String()}, unassigned(tk.ID),
			"%s: exactly one entry, naming who deleted the group", tk.Subject)
	}
	got, err := h.ticketSvc.GetByID(ctx, elsewhere.ID)
	require.NoError(t, err)
	require.NotNil(t, got.AssigneeGroupID)
	require.Empty(t, unassigned(elsewhere.ID))
}

// ── PATCH /admin/users/{id} ──────────────────────────────────────────────────

// Each step used to commit before the next started, so a request refused for
// one field had already applied the others. Nothing may change when any field
// is refused.
func TestPatchUser_RefusedRequestChangesNothing(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
	}{
		{"disable then bad email", map[string]any{"disabled": true, "email": "bad"}},
		{"disable then blank name", map[string]any{"disabled": true, "display_name": "   "}},
		{"disable then unknown role", map[string]any{"disabled": true, "role": "emperor"}},
		{"MFA reset then bad email", map[string]any{"reset_mfa": true, "email": "bad"}},
		{"demote then bad email", map[string]any{"role": "user", "email": "bad"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			ctx := context.Background()
			s := loggedInAdmin(t, h)
			enrollMFA(t, ctx, h.userSvc, h.staffID)
			before, err := h.userSvc.GetByIDAdmin(ctx, h.staffID)
			require.NoError(t, err)

			res, body := s.send(t, http.MethodPatch, adminAPI+"/users/"+h.staffID.String(), tc.body)
			res.Body.Close()
			require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", body)

			after, err := h.userSvc.GetByIDAdmin(ctx, h.staffID)
			require.NoError(t, err)
			require.Equal(t, before.Disabled, after.Disabled, "the account was disabled by a refused request")
			require.Equal(t, before.Role, after.Role, "the role was changed by a refused request")
			require.Equal(t, before.MFAEnabled, after.MFAEnabled, "MFA was reset by a refused request")
			require.Equal(t, before.Email, after.Email)
			require.Equal(t, before.DisplayName, after.DisplayName)
		})
	}
}

// Refusals that depend on the database's state, not the body, are also found
// before the first write (#419): a last-administrator guard and a taken email.
func TestPatchUser_LastAdminRefusalClearsNothingFirst(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	requireSoleAdministrator(t, h)
	s := loggedInAdmin(t, h)
	enrollMFA(t, ctx, h.userSvc, h.adminID)

	for _, body := range []map[string]any{
		{"reset_mfa": true, "role": "user"},
		{"reset_mfa": true, "disabled": true},
	} {
		res, out := s.send(t, http.MethodPatch, adminAPI+"/users/"+h.adminID.String(), body)
		res.Body.Close()
		require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", out)

		after, err := h.userSvc.GetByIDAdmin(ctx, h.adminID)
		require.NoError(t, err)
		require.True(t, after.MFAEnabled, "%v: MFA was cleared by a refused request", body)
		require.Equal(t, user.RoleAdmin, after.Role)
		require.False(t, after.Disabled)
	}
}

func TestPatchUser_TakenEmailIsRefusedBeforeAnyWrite(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	s := loggedInAdmin(t, h)
	enrollMFA(t, ctx, h.userSvc, h.staffID)
	holder, err := h.userSvc.GetByIDAdmin(ctx, h.userID)
	require.NoError(t, err)

	res, out := s.send(t, http.MethodPatch, adminAPI+"/users/"+h.staffID.String(),
		map[string]any{"disabled": true, "reset_mfa": true, "email": holder.Email})
	res.Body.Close()
	require.Equal(t, http.StatusConflict, res.StatusCode, "body: %s", out)

	after, err := h.userSvc.GetByIDAdmin(ctx, h.staffID)
	require.NoError(t, err)
	require.False(t, after.Disabled, "the account was disabled by a refused request")
	require.True(t, after.MFAEnabled, "MFA was cleared by a refused request")

	// Their own current address is not "taken".
	res, out = s.send(t, http.MethodPatch, adminAPI+"/users/"+h.staffID.String(),
		map[string]any{"email": after.Email, "display_name": "Same Address"})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", out)
}

// ── Deleted accounts ─────────────────────────────────────────────────────────

func deletedStaff(t *testing.T, h *harness) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	u, err := h.userSvc.Create(ctx, userInput("gone-"+uuid.NewString()[:8]+"@test.local", "Gone"))
	require.NoError(t, err)
	require.NoError(t, h.userSvc.Disable(ctx, u.ID))
	require.NoError(t, h.userSvc.SoftDelete(ctx, u.ID))
	return u.ID
}

// A session and an API key must get the same answer, in the same order: the
// body is checked first (400), then the account (404). Except on the password
// route, which is closed to keys altogether (2026-10-16): a key gets 403 there
// whatever the body and whatever the account.
func TestDeletedAccount_SessionAndAPIKeySeeTheSameAnswers(t *testing.T) {
	type send func(t *testing.T, h *harness, s *session, method, path string, body any) int
	senders := map[string]send{
		"session": func(t *testing.T, h *harness, s *session, method, path string, body any) int {
			res, _ := s.send(t, method, path, body)
			return res.StatusCode
		},
		"api key": func(t *testing.T, h *harness, _ *session, method, path string, body any) int {
			res := h.doAsAdmin(t, method, path, body)
			defer res.Body.Close()
			return res.StatusCode
		},
	}
	cases := []struct {
		name         string
		method, tail string
		body         any
		want         int
	}{
		{"password reset, valid body", http.MethodPost, "/password", map[string]any{"new_password": "a-brand-new-password"}, http.StatusNotFound},
		{"password reset, blank password", http.MethodPost, "/password", map[string]any{"new_password": ""}, http.StatusBadRequest},
		{"password reset, weak password", http.MethodPost, "/password", map[string]any{"new_password": "b"}, http.StatusBadRequest},
		{"patch, enable", http.MethodPatch, "", map[string]any{"disabled": false}, http.StatusNotFound},
		{"patch, rename", http.MethodPatch, "", map[string]any{"display_name": "New Name"}, http.StatusNotFound},
		{"patch, new email", http.MethodPatch, "", map[string]any{"email": "new@test.local"}, http.StatusNotFound},
		{"patch, bad email", http.MethodPatch, "", map[string]any{"email": "bad"}, http.StatusBadRequest},
	}
	for who, do := range senders {
		for _, tc := range cases {
			t.Run(who+"/"+tc.name, func(t *testing.T) {
				h, cleanup := newHarness(t)
				defer cleanup()
				ctx := context.Background()
				s := loggedInAdmin(t, h)
				id := deletedStaff(t, h)
				before, err := h.userSvc.GetByIDAdmin(ctx, id)
				require.NoError(t, err)

				want := tc.want
				if who == "api key" && tc.tail == "/password" {
					want = http.StatusForbidden
				}
				got := do(t, h, s, tc.method, adminAPI+"/users/"+id.String()+tc.tail, tc.body)
				require.Equal(t, want, got)

				after, err := h.userSvc.GetByIDAdmin(ctx, id)
				require.NoError(t, err)
				require.Equal(t, before.PasswordHash, after.PasswordHash, "a deleted account's password was set")
				require.Equal(t, before.Disabled, after.Disabled, "a deleted account was re-enabled")
				require.Equal(t, before.Email, after.Email)
				require.Equal(t, before.DisplayName, after.DisplayName)
			})
		}
	}
}

// The store statements refuse a deleted row themselves, so no caller can
// reach it by a path the handler forgot to guard.
func TestDeletedAccount_StoreWritesMatchNothing(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	id := deletedStaff(t, h)
	before, err := h.userSvc.GetByIDAdmin(ctx, id)
	require.NoError(t, err)

	require.ErrorIs(t, h.userSvc.AdminSetPassword(ctx, id, "a-brand-new-password", nil), user.ErrNotFound)
	require.ErrorIs(t, h.userSvc.UpdateProfile(ctx, id, "new@test.local", "New"), user.ErrNotFound)
	require.NoError(t, h.userSvc.Enable(ctx, id)) // :exec, a no-op rather than an error

	after, err := h.userSvc.GetByIDAdmin(ctx, id)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

// ── Path chains ──────────────────────────────────────────────────────────────

// Routes read only the deepest id, so /categories/X/types/Y acted on Y
// whatever X was.
func TestCTIPaths_AMismatchedChainAnswers404(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	c, tp, it := mkCTI(t, h)
	otherCat, otherType, _ := mkCTI(t, h)
	aCat := mkAssignment(t, h, customfield.CategoryScope, c.ID)
	aType := mkAssignment(t, h, customfield.TypeScope, tp.ID)
	aItem := mkAssignment(t, h, customfield.ItemScope, it.ID)

	cat, typ := "/categories/", "/types/"
	itm := "/items/"
	fld := "/fields/"
	def, err := h.customFieldSvc.CreateFieldDef(ctx, "F-"+uuid.NewString()[:8], customfield.FieldTypeText, nil, 1)
	require.NoError(t, err)
	newAssignment := map[string]any{"field_def_id": def.ID, "sort_order": 1}

	cases := []struct {
		name         string
		method, path string
		body         any
	}{
		{"patch type under the wrong category", http.MethodPatch, cat + otherCat.ID.String() + typ + tp.ID.String(), map[string]any{"name": "X"}},
		{"delete type under the wrong category", http.MethodDelete, cat + otherCat.ID.String() + typ + tp.ID.String(), nil},
		{"patch item under the wrong type", http.MethodPatch, cat + c.ID.String() + typ + otherType.ID.String() + itm + it.ID.String(), map[string]any{"name": "X"}},
		{"patch item under the wrong category", http.MethodPatch, cat + otherCat.ID.String() + typ + tp.ID.String() + itm + it.ID.String(), map[string]any{"name": "X"}},
		{"delete item under the wrong type", http.MethodDelete, cat + c.ID.String() + typ + otherType.ID.String() + itm + it.ID.String(), nil},
		{"list items under the wrong category", http.MethodGet, cat + otherCat.ID.String() + typ + tp.ID.String() + itm, nil},
		{"create item under the wrong category", http.MethodPost, cat + otherCat.ID.String() + typ + tp.ID.String() + itm, map[string]any{"name": "X"}},
		{"list type fields under the wrong category", http.MethodGet, cat + otherCat.ID.String() + typ + tp.ID.String() + fld, nil},
		{"list item fields under the wrong type", http.MethodGet, cat + c.ID.String() + typ + otherType.ID.String() + itm + it.ID.String() + fld, nil},
		{"create type field under the wrong category", http.MethodPost, cat + otherCat.ID.String() + typ + tp.ID.String() + fld, newAssignment},
		{"patch an assignment through the wrong scope", http.MethodPatch, cat + c.ID.String() + fld + aType.String(), map[string]any{"sort_order": 9}},
		{"patch a category assignment through its type", http.MethodPatch, cat + c.ID.String() + typ + tp.ID.String() + fld + aCat.String(), map[string]any{"sort_order": 9}},
		{"delete an assignment through the wrong scope", http.MethodDelete, cat + c.ID.String() + fld + aItem.String(), nil},
		{"delete an assignment through a sibling category", http.MethodDelete, cat + otherCat.ID.String() + fld + aCat.String(), nil},
		{"create on a category that does not exist", http.MethodPost, cat + uuid.NewString() + fld, newAssignment},
		{"create on a type that does not exist", http.MethodPost, cat + c.ID.String() + typ + uuid.NewString() + fld, newAssignment},
		{"create on an item that does not exist", http.MethodPost, cat + c.ID.String() + typ + tp.ID.String() + itm + uuid.NewString() + fld, newAssignment},
		{"list an unknown category's fields", http.MethodGet, cat + uuid.NewString() + fld, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := h.doAsAdmin(t, tc.method, adminAPI+tc.path, tc.body)
			res.Body.Close()
			require.Equal(t, http.StatusNotFound, res.StatusCode)
		})
	}

	// And none of them did anything.
	gotType, err := h.categorySvc.GetType(ctx, tp.ID)
	require.NoError(t, err)
	require.Equal(t, tp.Name, gotType.Name)
	gotItem, err := h.categorySvc.GetItem(ctx, it.ID)
	require.NoError(t, err)
	require.Equal(t, it.Name, gotItem.Name)
	for scope, id := range map[customfield.ScopeType]uuid.UUID{
		customfield.CategoryScope: c.ID, customfield.TypeScope: tp.ID, customfield.ItemScope: it.ID,
	} {
		require.Equal(t, 1, assignmentCount(t, h, scope, id), "%s assignment changed", scope)
	}
	gotAssignments, err := h.customFieldSvc.ListAssignments(ctx, customfield.TypeScope, tp.ID)
	require.NoError(t, err)
	require.Equal(t, 1, gotAssignments[0].SortOrder, "a refused PATCH changed the assignment")
}

// The correct chain still works, for every level.
func TestCTIPaths_ACorrectChainStillWorks(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c, tp, it := mkCTI(t, h)
	def, err := h.customFieldSvc.CreateFieldDef(context.Background(), "F-"+uuid.NewString()[:8], customfield.FieldTypeText, nil, 1)
	require.NoError(t, err)

	base := adminAPI + "/categories/" + c.ID.String()
	for _, p := range []string{
		base, base + "/types/" + tp.ID.String(), base + "/types/" + tp.ID.String() + "/items/" + it.ID.String(),
	} {
		res := h.doAsAdmin(t, http.MethodPatch, p, map[string]any{"name": "Renamed"})
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode, p)

		res = h.doAsAdmin(t, http.MethodPost, p+"/fields", map[string]any{"field_def_id": def.ID})
		res.Body.Close()
		require.Equal(t, http.StatusCreated, res.StatusCode, p)
		def, err = h.customFieldSvc.CreateFieldDef(context.Background(), "F-"+uuid.NewString()[:8], customfield.FieldTypeText, nil, 1)
		require.NoError(t, err)
	}
}
