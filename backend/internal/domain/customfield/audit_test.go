package customfield

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
)

// brief is an entry reduced to what these tests pin: its action and both sides.
type brief struct {
	Action        string
	Before, After map[string]any
}

func briefs(es []audit.Entry) []brief {
	if len(es) == 0 {
		return nil
	}
	out := make([]brief, len(es))
	for i, e := range es {
		out[i] = brief{e.Action, e.Before, e.After}
	}
	return out
}

func sel(name string, opts, retired []string) FieldDef {
	return FieldDef{ID: uuid.New(), Name: name, FieldType: FieldTypeSelect, Options: opts, RetiredOptions: retired, SortOrder: 1, Active: true}
}

func TestEntriesForCreated(t *testing.T) {
	d := sel("Dept", []string{"Sales", "EMEA"}, nil)
	d.SortOrder = 4
	actor := uuid.New()
	got := entriesForCreated(&actor, d)

	require.Equal(t, []brief{
		{"custom_field_created", nil, map[string]any{"field_name": "Dept", "field_type": "select", "sort_order": 4, "active": true}},
		{"custom_field_option_added", nil, map[string]any{"field_name": "Dept", "option_value": "Sales", "position": 1}},
		{"custom_field_option_added", nil, map[string]any{"field_name": "Dept", "option_value": "EMEA", "position": 2}},
	}, briefs(got))
	for _, e := range got {
		require.Equal(t, "custom_field", e.EntityType)
		require.Equal(t, d.ID, e.EntityID)
		require.Equal(t, &actor, e.ActorID)
		require.NotEqual(t, uuid.Nil, e.ID)
	}
}

// A given before/after pair of definitions produces exactly the expected
// entries, and nothing for a pair that differs in nothing that is audited.
func TestEntriesForChange(t *testing.T) {
	text := func(n string) FieldDef {
		return FieldDef{Name: n, FieldType: FieldTypeText, SortOrder: 1, Active: true}
	}

	for _, tc := range []struct {
		name          string
		before, after FieldDef
		want          []brief
	}{
		{"nothing changed", sel("F", []string{"a", "b"}, nil), sel("F", []string{"a", "b"}, nil), nil},
		{"renamed", text("Old"), func() FieldDef { d := text("New"); return d }(),
			[]brief{{"custom_field_renamed", map[string]any{"field_name": "Old"}, map[string]any{"field_name": "New"}}}},
		{"type changed", text("F"), func() FieldDef { d := text("F"); d.FieldType = FieldTypeNumber; return d }(),
			[]brief{{"custom_field_type_changed",
				map[string]any{"field_name": "F", "field_type": "text"}, map[string]any{"field_name": "F", "field_type": "number"}}}},
		{"deactivated", text("F"), func() FieldDef { d := text("F"); d.Active = false; return d }(),
			[]brief{{"custom_field_deactivated", map[string]any{"field_name": "F", "active": true}, map[string]any{"field_name": "F", "active": false}}}},
		{"reactivated", func() FieldDef { d := text("F"); d.Active = false; return d }(), text("F"),
			[]brief{{"custom_field_reactivated", map[string]any{"field_name": "F", "active": false}, map[string]any{"field_name": "F", "active": true}}}},
		{"number changed", text("F"), func() FieldDef { d := text("F"); d.SortOrder = 9; return d }(),
			[]brief{{"custom_field_reordered", map[string]any{"field_name": "F", "sort_order": 1}, map[string]any{"field_name": "F", "sort_order": 9}}}},
		{"option added at the end", sel("F", []string{"a"}, nil), sel("F", []string{"a", "b"}, nil),
			[]brief{{"custom_field_option_added", nil, map[string]any{"field_name": "F", "option_value": "b", "position": 2}}}},
		{"option added at the front moves nobody", sel("F", []string{"a", "b"}, nil), sel("F", []string{"z", "a", "b"}, nil),
			[]brief{{"custom_field_option_added", nil, map[string]any{"field_name": "F", "option_value": "z", "position": 1}}}},
		{"one option dragged to the top is one move, not three", sel("F", []string{"a", "b", "c"}, nil), sel("F", []string{"c", "a", "b"}, nil),
			[]brief{
				{"custom_field_option_moved",
					map[string]any{"field_name": "F", "option_value": "c", "position": 3}, map[string]any{"field_name": "F", "option_value": "c", "position": 1}},
			}},
		{"one option dragged to the bottom", sel("F", []string{"a", "b", "c", "d"}, nil), sel("F", []string{"b", "c", "d", "a"}, nil),
			[]brief{
				{"custom_field_option_moved",
					map[string]any{"field_name": "F", "option_value": "a", "position": 1}, map[string]any{"field_name": "F", "option_value": "a", "position": 4}},
			}},
		{"an added option does not make the rest move", sel("F", []string{"a", "b", "c"}, nil), sel("F", []string{"c", "z", "a", "b"}, nil),
			[]brief{
				{"custom_field_option_added", nil, map[string]any{"field_name": "F", "option_value": "z", "position": 2}},
				{"custom_field_option_moved",
					map[string]any{"field_name": "F", "option_value": "c", "position": 3}, map[string]any{"field_name": "F", "option_value": "c", "position": 1}},
			}},
		{"option retired", sel("F", []string{"a", "b"}, nil), sel("F", []string{"a"}, []string{"b"}),
			[]brief{{"custom_field_option_retired",
				map[string]any{"field_name": "F", "option_value": "b", "retired": false}, map[string]any{"field_name": "F", "option_value": "b", "retired": true}}}},
		{"option restored", sel("F", []string{"a"}, []string{"b"}), sel("F", []string{"a", "b"}, nil),
			[]brief{{"custom_field_option_restored",
				map[string]any{"field_name": "F", "option_value": "b", "retired": true}, map[string]any{"field_name": "F", "option_value": "b", "retired": false}}}},
		{"option deleted", sel("F", []string{"a", "b"}, nil), sel("F", []string{"a"}, nil),
			[]brief{{"custom_field_option_deleted", map[string]any{"field_name": "F", "option_value": "b"}, nil}}},
		{"retired option deleted", sel("F", []string{"a"}, []string{"b"}), sel("F", []string{"a"}, nil),
			[]brief{{"custom_field_option_deleted", map[string]any{"field_name": "F", "option_value": "b"}, nil}}},
		{"rename of an unused option is a delete and an add", sel("F", []string{"a", "b"}, nil), sel("F", []string{"a", "c"}, nil),
			[]brief{
				{"custom_field_option_added", nil, map[string]any{"field_name": "F", "option_value": "c", "position": 2}},
				{"custom_field_option_deleted", map[string]any{"field_name": "F", "option_value": "b"}, nil},
			}},
		{"rename of a held option retires the old text and adds the new", sel("F", []string{"a", "b"}, nil), sel("F", []string{"a", "c"}, []string{"b"}),
			[]brief{
				{"custom_field_option_added", nil, map[string]any{"field_name": "F", "option_value": "c", "position": 2}},
				{"custom_field_option_retired",
					map[string]any{"field_name": "F", "option_value": "b", "retired": false}, map[string]any{"field_name": "F", "option_value": "b", "retired": true}},
			}},
		{"a rename and a deactivation are two entries, the later ones under the new name",
			text("Old"), func() FieldDef { d := text("New"); d.Active = false; return d }(),
			[]brief{
				{"custom_field_renamed", map[string]any{"field_name": "Old"}, map[string]any{"field_name": "New"}},
				{"custom_field_deactivated", map[string]any{"field_name": "New", "active": true}, map[string]any{"field_name": "New", "active": false}},
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.before.ID = uuid.New()
			tc.after.ID = tc.before.ID
			got := entriesForChange(nil, tc.before, tc.after)
			require.Equal(t, tc.want, briefs(got))
			for _, e := range got {
				require.Equal(t, "custom_field", e.EntityType)
				require.Equal(t, tc.before.ID, e.EntityID)
			}
		})
	}
}

func TestEntriesForReorder_OnlyMovedFields(t *testing.T) {
	a := FieldDef{ID: uuid.New(), Name: "A", SortOrder: 1}
	b := FieldDef{ID: uuid.New(), Name: "B", SortOrder: 2}
	c := FieldDef{ID: uuid.New(), Name: "C", SortOrder: 0}
	// The new order is A, B, C, so the numbers become 1, 2, 3. A and B hold them
	// already; only C (0 → 3) changes.
	got := entriesForReorder(nil, []FieldDef{a, b, c}, []uuid.UUID{a.ID, b.ID, c.ID})
	require.Equal(t, []brief{
		{"custom_field_reordered", map[string]any{"field_name": "C", "sort_order": 0}, map[string]any{"field_name": "C", "sort_order": 3}},
	}, briefs(got), "A and B already hold positions 1 and 2")
}

func TestAssignmentEntries(t *testing.T) {
	field, scope := uuid.New(), uuid.New()
	a := Assignment{ID: uuid.New(), FieldDefID: field, ScopeType: TypeScope, ScopeID: scope, SortOrder: 3, VisibleOnNew: true}
	info := AssignmentInfo{FieldName: "Dept", ScopePath: "Hardware › Laptop"}
	keys := func(a Assignment, sortOrder bool) map[string]any {
		m := map[string]any{
			"field_name": "Dept", "assignment_id": a.ID.String(), "scope_type": "type", "scope_id": scope.String(),
			"scope_path": "Hardware › Laptop", "visible_on_new": a.VisibleOnNew, "required_on_new": a.RequiredOnNew,
		}
		if sortOrder {
			m["sort_order"] = a.SortOrder
		}
		return m
	}

	attached := entryAttached(nil, info, a)
	require.Equal(t, brief{"custom_field_attached", nil, keys(a, false)}, briefs([]audit.Entry{attached})[0])
	require.Equal(t, field, attached.EntityID, "an attachment is part of its field's history")

	after := a
	after.RequiredOnNew = true
	after.SortOrder = 5
	changed := entriesForAttachmentChange(nil, info, a, after)
	require.Equal(t, []brief{{"custom_field_attachment_changed", keys(a, true), keys(after, true)}}, briefs(changed))

	require.Empty(t, entriesForAttachmentChange(nil, info, a, a), "nothing changed, nothing recorded")

	detached := entryDetached(nil, info, a)
	require.Equal(t, brief{"custom_field_detached", keys(a, false), nil}, briefs([]audit.Entry{detached})[0])
}
