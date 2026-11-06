package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
)

// Every administrator change to a custom field, its options or its attachments
// is audited (#603), as entity "custom_field" with the field's id, so a field's
// whole history is one entity.

// fieldHistory is the field's audit entries, oldest first.
func (h *harness) fieldHistory(t *testing.T, id uuid.UUID) []audit.Entry {
	t.Helper()
	es, err := h.auditStore.ListByEntity(context.Background(), "custom_field", id, 500, 0)
	require.NoError(t, err)
	slices.Reverse(es)
	return es
}

func actionsOf(es []audit.Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Action
	}
	sort.Strings(out)
	return out
}

// shown is an entry's two sides as the audit view shows them.
func shown(e audit.Entry) (before, after map[string]any) { return audit.Redact(e.Before, e.After) }

// step runs one change and returns the entries it wrote. Entries written in the
// same statement can share a timestamp, so callers compare them as a set.
type historian struct {
	t *testing.T
	h *harness
	n int
	// id of the field whose history is read
	id uuid.UUID
}

func (hs *historian) since() []audit.Entry {
	hs.t.Helper()
	all := hs.h.fieldHistory(hs.t, hs.id)
	out := all[hs.n:]
	hs.n = len(all)
	return out
}

func only(t *testing.T, es []audit.Entry, action string) audit.Entry {
	t.Helper()
	require.Equal(t, []string{action}, actionsOf(es))
	return es[0]
}

// One test per action, through the HTTP routes, checking the entity, the actor
// and what the audit view shows of each side.
func TestCustomFieldAudit_EveryAction(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c, tp, it := mkCTI(t, h)

	// created: the field, and one entry per option.
	code, body := h.cfKey(t, http.MethodPost, cfAdmin, map[string]any{
		"name": "Dept", "field_type": "select", "options": []string{"Sales", "EMEA"}, "sort_order": 4,
	})
	require.Equal(t, http.StatusCreated, code, string(body))
	var def customfield.FieldDef
	require.NoError(t, json.Unmarshal(body, &def))
	hs := &historian{t: t, h: h, id: def.ID}
	got := hs.since()
	require.Equal(t, []string{"custom_field_created", "custom_field_option_added", "custom_field_option_added"}, actionsOf(got))
	for _, e := range got {
		require.Equal(t, "custom_field", e.EntityType)
		require.Equal(t, def.ID, e.EntityID)
		require.NotNil(t, e.ActorID)
		require.Equal(t, h.adminID, *e.ActorID, "the administrator who made the change")
	}
	for _, e := range got {
		if e.Action == "custom_field_created" {
			b, a := shown(e)
			require.Nil(t, b)
			require.Equal(t, "Dept", a["field_name"])
			require.Equal(t, "select", a["field_type"])
			require.EqualValues(t, 4, a["sort_order"])
			require.Equal(t, true, a["active"])
		}
	}

	patch := func(body map[string]any) []audit.Entry {
		t.Helper()
		code, out := h.patchField(t, def.ID, body)
		require.Equal(t, http.StatusOK, code, string(out))
		return hs.since()
	}

	e := only(t, patch(map[string]any{"name": "Department"}), "custom_field_renamed")
	b, a := shown(e)
	require.Equal(t, map[string]any{"field_name": "Dept"}, b)
	require.Equal(t, map[string]any{"field_name": "Department"}, a)

	e = only(t, patch(map[string]any{"sort_order": 9}), "custom_field_reordered")
	b, a = shown(e)
	require.EqualValues(t, map[string]any{"field_name": "Department", "sort_order": float64(4)}, b)
	require.EqualValues(t, map[string]any{"field_name": "Department", "sort_order": float64(9)}, a)

	e = only(t, patch(map[string]any{"options": []string{"Sales", "EMEA", "APAC"}}), "custom_field_option_added")
	b, a = shown(e)
	require.Nil(t, b)
	require.Equal(t, map[string]any{"field_name": "Department", "option_value": "APAC", "position": float64(3)}, a)

	e = only(t, patch(map[string]any{"options": []string{"APAC", "Sales", "EMEA"}}), "custom_field_option_moved")
	b, a = shown(e)
	require.Equal(t, map[string]any{"field_name": "Department", "option_value": "APAC", "position": float64(3)}, b)
	require.Equal(t, map[string]any{"field_name": "Department", "option_value": "APAC", "position": float64(1)}, a)
	patch(map[string]any{"options": []string{"EMEA", "Sales", "APAC"}}) // back, for the steps below (one move)

	e = only(t, patch(map[string]any{"options": []string{"EMEA", "APAC"}, "retired_options": []string{"Sales"}}), "custom_field_option_retired")
	b, a = shown(e)
	require.Equal(t, map[string]any{"field_name": "Department", "option_value": "Sales", "retired": false}, b)
	require.Equal(t, map[string]any{"field_name": "Department", "option_value": "Sales", "retired": true}, a)

	e = only(t, patch(map[string]any{"options": []string{"EMEA", "APAC", "Sales"}, "retired_options": []string{}}), "custom_field_option_restored")
	b, a = shown(e)
	require.Equal(t, true, b["retired"])
	require.Equal(t, false, a["retired"])

	e = only(t, patch(map[string]any{"options": []string{"EMEA", "APAC"}}), "custom_field_option_deleted")
	b, a = shown(e)
	require.Equal(t, map[string]any{"field_name": "Department", "option_value": "Sales"}, b)
	require.Nil(t, a)

	e = only(t, patch(map[string]any{"active": false}), "custom_field_deactivated")
	b, a = shown(e)
	require.Equal(t, true, b["active"])
	require.Equal(t, false, a["active"])
	e = only(t, patch(map[string]any{"active": true}), "custom_field_reactivated")
	b, a = shown(e)
	require.Equal(t, false, b["active"])
	require.Equal(t, true, a["active"])

	// type_changed needs a field that is unused and not a select: its own.
	text := h.mkField(t, "Serial", customfield.FieldTypeText, nil, 1)
	ths := &historian{t: t, h: h, id: text.ID}
	ths.since()
	code, out := h.patchField(t, text.ID, map[string]any{"field_type": "number"})
	require.Equal(t, http.StatusOK, code, string(out))
	e = only(t, ths.since(), "custom_field_type_changed")
	b, a = shown(e)
	require.Equal(t, map[string]any{"field_name": "Serial", "field_type": "text"}, b)
	require.Equal(t, map[string]any{"field_name": "Serial", "field_type": "number"}, a)

	// attached / attachment_changed / detached, from the Categories routes.
	base := adminAPI + "/categories/" + c.ID.String()
	typePath := base + "/types/" + tp.ID.String() + "/fields"
	code, out = h.cfKey(t, http.MethodPost, typePath, map[string]any{"field_def_id": def.ID, "sort_order": 3, "visible_on_new": true})
	require.Equal(t, http.StatusCreated, code, string(out))
	var asg customfield.Assignment
	require.NoError(t, json.Unmarshal(out, &asg))
	e = only(t, hs.since(), "custom_field_attached")
	b, a = shown(e)
	require.Equal(t, h.adminID, *e.ActorID)
	require.Nil(t, b)
	require.Equal(t, map[string]any{
		"field_name": "Department", "assignment_id": asg.ID.String(), "scope_type": "type", "scope_id": tp.ID.String(),
		"scope_path": c.Name + " › Type", "visible_on_new": true, "required_on_new": false,
	}, a)

	code, out = h.cfKey(t, http.MethodPatch, typePath+"/"+asg.ID.String(), map[string]any{"required_on_new": true, "sort_order": 5})
	require.Equal(t, http.StatusOK, code, string(out))
	e = only(t, hs.since(), "custom_field_attachment_changed")
	b, a = shown(e)
	require.Equal(t, false, b["required_on_new"])
	require.EqualValues(t, 3, b["sort_order"])
	require.Equal(t, true, a["required_on_new"])
	require.EqualValues(t, 5, a["sort_order"])
	require.Equal(t, c.Name+" › Type", a["scope_path"])

	// An item-level path names all three levels.
	itemPath := base + "/types/" + tp.ID.String() + "/items/" + it.ID.String() + "/fields"
	code, out = h.cfKey(t, http.MethodPost, itemPath, map[string]any{"field_def_id": def.ID})
	require.Equal(t, http.StatusCreated, code, string(out))
	e = only(t, hs.since(), "custom_field_attached")
	_, a = shown(e)
	require.Equal(t, c.Name+" › Type › Item", a["scope_path"])

	code, out = h.cfKey(t, http.MethodDelete, typePath+"/"+asg.ID.String(), nil)
	require.Equal(t, http.StatusNoContent, code, string(out))
	e = only(t, hs.since(), "custom_field_detached")
	b, a = shown(e)
	require.Equal(t, asg.ID.String(), b["assignment_id"])
	require.Equal(t, c.Name+" › Type", b["scope_path"])
	require.NotContains(t, b, "node_deleted", "removed through its route, not by a node delete")
	require.Nil(t, a)

	// An attachment change that changes nothing writes nothing.
	code, out = h.cfKey(t, http.MethodPost, base+"/fields", map[string]any{"field_def_id": text.ID, "visible_on_new": true})
	require.Equal(t, http.StatusCreated, code, string(out))
	var same customfield.Assignment
	require.NoError(t, json.Unmarshal(out, &same))
	ths.since()
	code, out = h.cfKey(t, http.MethodPatch, base+"/fields/"+same.ID.String(), map[string]any{"visible_on_new": true})
	require.Equal(t, http.StatusOK, code, string(out))
	require.Empty(t, ths.since())

	// And none of it was done by the system.
	for _, e := range h.fieldHistory(t, def.ID) {
		require.NotNil(t, e.ActorID, e.Action)
	}
}

// A change that is refused writes no entry: the 409s, and a 400.
func TestCustomFieldAudit_RefusedChangeWritesNoEntry(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sel := h.mkField(t, "Brand", customfield.FieldTypeSelect, []string{"Dell", "HP"}, 1)
	h.holds(t, sel, "HP")
	hs := &historian{t: t, h: h, id: sel.ID}
	hs.since()

	for name, body := range map[string]map[string]any{
		"an option a ticket holds":        {"options": []string{"Dell"}},
		"the type, once a value exists":   {"field_type": "text"},
		"a rename together with a delete": {"name": "Renamed", "options": []string{"Dell"}},
		"an invalid change":               {"options": []string{}},
	} {
		code, out := h.patchField(t, sel.ID, body)
		require.GreaterOrEqual(t, code, 400, "%s: %s", name, out)
		require.Empty(t, hs.since(), "%s: nothing was recorded", name)
	}
}

func TestCustomFieldAudit_ReorderWritesOnlyMovedFields(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	a := h.mkField(t, "A", customfield.FieldTypeText, nil, 1)
	b := h.mkField(t, "B", customfield.FieldTypeText, nil, 2)
	c := h.mkField(t, "C", customfield.FieldTypeText, nil, 3)
	d := h.mkField(t, "D", customfield.FieldTypeText, nil, 9)

	// A and B keep positions 1 and 2; D moves 9 → 3 and C moves 3 → 4.
	code, body := h.cf(t, http.MethodPut, cfAdmin+"/order", map[string]any{"field_def_ids": []uuid.UUID{a.ID, b.ID, d.ID, c.ID}})
	require.Equal(t, http.StatusOK, code, string(body))

	require.Equal(t, []string{"custom_field_created"}, actionsOf(h.fieldHistory(t, a.ID)), "A did not move: only its creation is recorded")
	require.Equal(t, []string{"custom_field_created"}, actionsOf(h.fieldHistory(t, b.ID)), "B did not move: only its creation is recorded")
	for id, want := range map[uuid.UUID][2]float64{d.ID: {9, 3}, c.ID: {3, 4}} {
		es := h.fieldHistory(t, id)
		require.Len(t, es, 2)
		e := es[1]
		require.Equal(t, "custom_field_reordered", e.Action)
		before, after := shown(e)
		require.Equal(t, want[0], before["sort_order"])
		require.Equal(t, want[1], after["sort_order"])
		require.Equal(t, h.adminID, *e.ActorID)
	}

	// The same order again moves nobody.
	code, body = h.cf(t, http.MethodPut, cfAdmin+"/order", map[string]any{"field_def_ids": []uuid.UUID{a.ID, b.ID, d.ID, c.ID}})
	require.Equal(t, http.StatusOK, code, string(body))
	require.Len(t, h.fieldHistory(t, d.ID), 2)
}

// The Categories page's own routes are audited too, at every level.
func TestCustomFieldAudit_AttachFromCategoriesRouteIsAudited(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c, tp, it := mkCTI(t, h)
	def := h.mkField(t, "F", customfield.FieldTypeText, nil, 1)
	base := adminAPI + "/categories/" + c.ID.String()

	for scope, tc := range map[string]struct{ path, wantPath string }{
		"category": {base + "/fields", c.Name},
		"type":     {base + "/types/" + tp.ID.String() + "/fields", c.Name + " › Type"},
		"item":     {base + "/types/" + tp.ID.String() + "/items/" + it.ID.String() + "/fields", c.Name + " › Type › Item"},
	} {
		t.Run(scope, func(t *testing.T) {
			before := len(h.fieldHistory(t, def.ID))
			code, out := h.cfKey(t, http.MethodPost, tc.path, map[string]any{"field_def_id": def.ID, "visible_on_new": true})
			require.Equal(t, http.StatusCreated, code, string(out))
			es := h.fieldHistory(t, def.ID)
			require.Len(t, es, before+1)
			e := es[len(es)-1]
			require.Equal(t, "custom_field_attached", e.Action)
			_, a := shown(e)
			require.Equal(t, scope, a["scope_type"])
			require.Equal(t, tc.wantPath, a["scope_path"])
		})
	}
}

// Deleting a category, type or item takes its attachments, and each is
// recorded as detached with node_deleted, the actor, and the path as it was.
func TestDeleteCTINode_AuditsEachRemovedAttachment(t *testing.T) {
	for _, tc := range []struct {
		name string
		path func(c, tp, it uuid.UUID) string
		// the scopes whose attachments the delete removes
		removed []customfield.ScopeType
	}{
		{"item", func(c, tp, it uuid.UUID) string {
			return adminAPI + "/categories/" + c.String() + "/types/" + tp.String() + "/items/" + it.String()
		}, []customfield.ScopeType{customfield.ItemScope}},
		{"type, and the items under it", func(c, tp, it uuid.UUID) string {
			return adminAPI + "/categories/" + c.String() + "/types/" + tp.String()
		}, []customfield.ScopeType{customfield.TypeScope, customfield.ItemScope}},
		{"category, and everything under it", func(c, tp, it uuid.UUID) string { return adminAPI + "/categories/" + c.String() },
			[]customfield.ScopeType{customfield.CategoryScope, customfield.TypeScope, customfield.ItemScope}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			c, tp, it := mkCTI(t, h)
			scopeIDs := map[customfield.ScopeType]uuid.UUID{customfield.CategoryScope: c.ID, customfield.TypeScope: tp.ID, customfield.ItemScope: it.ID}
			wantPath := map[customfield.ScopeType]string{
				customfield.CategoryScope: c.Name, customfield.TypeScope: c.Name + " › Type", customfield.ItemScope: c.Name + " › Type › Item",
			}
			// A field per scope, so each entry is on a different entity.
			defs := map[customfield.ScopeType]customfield.FieldDef{}
			asg := map[customfield.ScopeType]customfield.Assignment{}
			for scope, id := range scopeIDs {
				defs[scope] = h.mkField(t, "F "+string(scope), customfield.FieldTypeText, nil, 1)
				asg[scope] = h.attach(t, defs[scope], scope, id, 4)
			}
			attachedEntries := map[customfield.ScopeType]int{}
			for scope := range scopeIDs {
				attachedEntries[scope] = len(h.fieldHistory(t, defs[scope].ID))
			}

			code, out := h.cfKey(t, http.MethodDelete, tc.path(c.ID, tp.ID, it.ID), nil)
			require.Equal(t, http.StatusNoContent, code, string(out))

			for scope := range scopeIDs {
				es := h.fieldHistory(t, defs[scope].ID)
				if !slices.Contains(tc.removed, scope) {
					require.Len(t, es, attachedEntries[scope], "%s attachments are not touched", scope)
					continue
				}
				require.Len(t, es, attachedEntries[scope]+1, "one entry per attachment removed")
				// By action, not by position: the entry is stamped by the database clock, the
				// ones before it by this process's.
				idx := slices.IndexFunc(es, func(e audit.Entry) bool { return e.Action == "custom_field_detached" })
				require.GreaterOrEqual(t, idx, 0)
				e := es[idx]
				require.Equal(t, "custom_field", e.EntityType)
				require.Equal(t, defs[scope].ID, e.EntityID)
				require.NotNil(t, e.ActorID)
				require.Equal(t, h.adminID, *e.ActorID, "the administrator who deleted the node")
				before, after := shown(e)
				require.Nil(t, after)
				require.Equal(t, map[string]any{
					"field_name": "F " + string(scope), "assignment_id": asg[scope].ID.String(),
					"scope_type": string(scope), "scope_id": scopeIDs[scope].String(), "scope_path": wantPath[scope],
					"visible_on_new": true, "required_on_new": false, "sort_order": float64(4), "node_deleted": true,
				}, before, "the path as it was before the node went")
			}
		})
	}
}

// A delete that is refused (the category has tickets) writes no entry, because
// the entries and the delete are one statement.
func TestDeleteCTINode_RefusedDeleteWritesNoAuditEntry(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c, tp, _ := mkCTI(t, h)
	def := h.mkField(t, "F", customfield.FieldTypeText, nil, 1)
	h.attach(t, def, customfield.CategoryScope, c.ID, 1)
	h.attach(t, def, customfield.TypeScope, tp.ID, 1)
	ticketAt(t, h, c, &tp, nil) // makes the category and the type undeletable
	before := len(h.fieldHistory(t, def.ID))

	for _, path := range []string{
		adminAPI + "/categories/" + c.ID.String(),
		adminAPI + "/categories/" + c.ID.String() + "/types/" + tp.ID.String(),
	} {
		code, out := h.cfKey(t, http.MethodDelete, path, nil)
		require.Equal(t, http.StatusConflict, code, string(out))
	}
	require.Len(t, h.fieldHistory(t, def.ID), before, "no entry for a delete that did not happen")
	require.Equal(t, 1, assignmentCount(t, h, customfield.CategoryScope, c.ID))
	require.Equal(t, 1, assignmentCount(t, h, customfield.TypeScope, tp.ID))
}

// The audit page shows the field's name, an option's text and the path, and
// nothing that is not on the allow-list.
func TestCustomFieldAudit_TheAdminAuditViewShowsNamesAndPaths(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c, tp, _ := mkCTI(t, h)
	def := h.mkField(t, "Dept", customfield.FieldTypeSelect, []string{"Sales"}, 1)
	_, err := h.customFieldSvc.CreateAssignment(context.Background(), def.ID, customfield.TypeScope, tp.ID, 1, true, false, &h.adminID)
	require.NoError(t, err)
	code, out := h.patchField(t, def.ID, map[string]any{"options": []string{"Sales", "Marketing"}})
	require.Equal(t, http.StatusOK, code, string(out))

	code, body := h.cfKey(t, http.MethodGet, adminAPI+"/audit?entity_type=custom_field", nil)
	require.Equal(t, http.StatusOK, code, string(body))
	for _, want := range []string{`"field_name":"Dept"`, `"option_value":"Marketing"`, `"scope_path":"` + c.Name + ` › Type"`} {
		require.Contains(t, string(body), want)
	}
	require.NotContains(t, string(body), "[redacted]")
}
