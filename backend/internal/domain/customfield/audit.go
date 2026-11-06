package customfield

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
)

// Every administrator change to a field definition, its options or its
// attachments is audited, in the same transaction as the change (the store
// writes these entries where it writes the change). The functions here decide
// what the entries say; they are pure, so a before/after pair can be tested
// without a database.
//
// The entity is the field definition, attachments included, so a field's whole
// history is one entity. Every entry carries field_name on both sides, even when
// the name did not change, because the audit page hides keys that did not change
// and would otherwise not say which field an entry is about. Option entries
// carry the option's text as a single value (option_value) and not the list,
// because the audit view drops any value that is an array. Attachment entries
// carry scope_path, a snapshot such as "Hardware › Laptop", so the entry still
// reads correctly after the node is renamed or deleted.
//
// field_name, option_value and scope_path are text an administrator wrote and
// never ticket content, which is what lets audit.shownKeys show them.
const auditEntityType = "custom_field"

type actorKey struct{}

// WithActor returns ctx carrying the administrator whose change this is, for
// the audit entries the Service writes. The Service methods take no actor of
// their own so that a caller with none (a test, a job) is audited as the system
// rather than being unable to call them.
func WithActor(ctx context.Context, userID uuid.UUID) context.Context {
	return context.WithValue(ctx, actorKey{}, userID)
}

func actorFrom(ctx context.Context) *uuid.UUID {
	if id, ok := ctx.Value(actorKey{}).(uuid.UUID); ok {
		return &id
	}
	return nil
}

// AssignmentInfo is what an attachment's audit entry says beyond the
// attachment itself: the field's name and where it is attached, read in the
// transaction that writes the entry.
type AssignmentInfo struct {
	FieldName string
	ScopePath string
}

func newEntry(actor *uuid.UUID, fieldID uuid.UUID, action string, before, after map[string]any) audit.Entry {
	return audit.Entry{
		ID: uuid.New(), ActorID: actor, EntityType: auditEntityType, EntityID: fieldID,
		Action: action, Before: before, After: after, CreatedAt: time.Now(),
	}
}

func entriesForCreated(actor *uuid.UUID, d FieldDef) []audit.Entry {
	out := []audit.Entry{newEntry(actor, d.ID, "custom_field_created", nil, map[string]any{
		"field_name": d.Name, "field_type": string(d.FieldType), "sort_order": d.SortOrder, "active": d.Active,
	})}
	for i, o := range d.Options {
		out = append(out, newEntry(actor, d.ID, "custom_field_option_added", nil, optionKeys(d.Name, o, i+1)))
	}
	return out
}

func optionKeys(field, option string, position int) map[string]any {
	return map[string]any{"field_name": field, "option_value": option, "position": position}
}

func retiredKeys(field, option string, retired bool) map[string]any {
	return map[string]any{"field_name": field, "option_value": option, "retired": retired}
}

// entriesForChange is what changed between two states of one definition. The
// server cannot tell a rename of an option from a delete plus an add, so it
// records the second; nothing here is guessing at intent.
func entriesForChange(actor *uuid.UUID, before, after FieldDef) []audit.Entry {
	var out []audit.Entry
	add := func(action string, b, a map[string]any) {
		out = append(out, newEntry(actor, after.ID, action, b, a))
	}
	n := after.Name

	if before.Name != after.Name {
		add("custom_field_renamed", map[string]any{"field_name": before.Name}, map[string]any{"field_name": after.Name})
	}
	if before.FieldType != after.FieldType {
		add("custom_field_type_changed",
			map[string]any{"field_name": n, "field_type": string(before.FieldType)},
			map[string]any{"field_name": n, "field_type": string(after.FieldType)})
	}
	if before.Active != after.Active {
		action := "custom_field_deactivated"
		if after.Active {
			action = "custom_field_reactivated"
		}
		add(action, map[string]any{"field_name": n, "active": before.Active}, map[string]any{"field_name": n, "active": after.Active})
	}
	if before.SortOrder != after.SortOrder {
		add("custom_field_reordered",
			map[string]any{"field_name": n, "sort_order": before.SortOrder},
			map[string]any{"field_name": n, "sort_order": after.SortOrder})
	}

	known := func(d FieldDef, v string) bool {
		return slices.Contains(d.Options, v) || slices.Contains(d.RetiredOptions, v)
	}

	for i, o := range after.Options {
		if !known(before, o) {
			add("custom_field_option_added", nil, optionKeys(n, o, i+1))
		}
	}
	// Moved: judged among the options both lists have, so adding or removing
	// one does not report every option after it as moved. The positions
	// recorded are in the full lists.
	for _, o := range movedOptions(common(before.Options, after.Options), common(after.Options, before.Options)) {
		add("custom_field_option_moved",
			optionKeys(n, o, slices.Index(before.Options, o)+1),
			optionKeys(n, o, slices.Index(after.Options, o)+1))
	}
	for _, o := range after.RetiredOptions {
		if slices.Contains(before.Options, o) {
			add("custom_field_option_retired", retiredKeys(n, o, false), retiredKeys(n, o, true))
		}
	}
	for _, o := range after.Options {
		if slices.Contains(before.RetiredOptions, o) {
			add("custom_field_option_restored", retiredKeys(n, o, true), retiredKeys(n, o, false))
		}
	}
	for _, o := range append(slices.Clone(before.Options), before.RetiredOptions...) {
		if !known(after, o) {
			add("custom_field_option_deleted", map[string]any{"field_name": n, "option_value": o}, nil)
		}
	}
	return out
}

// common is the values of a that b also has, in a's order.
func common(a, b []string) []string {
	var out []string
	for _, v := range a {
		if slices.Contains(b, v) {
			out = append(out, v)
		}
	}
	return out
}

// entriesForReorder is one entry per field whose number the new order changes.
// before is every definition as it stands; ids is the new order, position n
// becoming number n.
func entriesForReorder(actor *uuid.UUID, before []FieldDef, ids []uuid.UUID) []audit.Entry {
	byID := make(map[uuid.UUID]FieldDef, len(before))
	for _, d := range before {
		byID[d.ID] = d
	}
	var out []audit.Entry
	for i, id := range ids {
		d := byID[id]
		if d.SortOrder == i+1 {
			continue
		}
		out = append(out, newEntry(actor, id, "custom_field_reordered",
			map[string]any{"field_name": d.Name, "sort_order": d.SortOrder},
			map[string]any{"field_name": d.Name, "sort_order": i + 1}))
	}
	return out
}

func attachmentKeys(info AssignmentInfo, a Assignment) map[string]any {
	return map[string]any{
		"field_name": info.FieldName, "assignment_id": a.ID.String(),
		"scope_type": string(a.ScopeType), "scope_id": a.ScopeID.String(), "scope_path": info.ScopePath,
		"visible_on_new": a.VisibleOnNew, "required_on_new": a.RequiredOnNew,
	}
}

func entryAttached(actor *uuid.UUID, info AssignmentInfo, a Assignment) audit.Entry {
	return newEntry(actor, a.FieldDefID, "custom_field_attached", nil, attachmentKeys(info, a))
}

func entriesForAttachmentChange(actor *uuid.UUID, info AssignmentInfo, before, after Assignment) []audit.Entry {
	if before.VisibleOnNew == after.VisibleOnNew && before.RequiredOnNew == after.RequiredOnNew && before.SortOrder == after.SortOrder {
		return nil
	}
	b, a := attachmentKeys(info, before), attachmentKeys(info, after)
	b["sort_order"], a["sort_order"] = before.SortOrder, after.SortOrder
	return []audit.Entry{newEntry(actor, after.FieldDefID, "custom_field_attachment_changed", b, a)}
}

func entryDetached(actor *uuid.UUID, info AssignmentInfo, a Assignment) audit.Entry {
	return newEntry(actor, a.FieldDefID, "custom_field_detached", attachmentKeys(info, a), nil)
}

// movedOptions is the options of the new order that were moved, among those
// both orders have: the fewest, which are the ones outside a longest run that
// kept its relative order. Dragging one option to the top of ten is one move, not
// ten. Where two runs are equally long the choice is fixed by the algorithm, so
// the same change always reads the same.
func movedOptions(oldOrder, newOrder []string) []string {
	pos := make([]int, len(newOrder)) // where each of the new order was in the old
	for i, v := range newOrder {
		pos[i] = slices.Index(oldOrder, v)
	}
	// Longest increasing subsequence of pos, by patience sorting: tails[k] is the
	// index in newOrder ending the best run of length k+1, prev links it back.
	var tails []int
	prev := make([]int, len(pos))
	for i, p := range pos {
		k := sort.Search(len(tails), func(k int) bool { return pos[tails[k]] >= p })
		prev[i] = -1
		if k > 0 {
			prev[i] = tails[k-1]
		}
		if k == len(tails) {
			tails = append(tails, i)
		} else {
			tails[k] = i
		}
	}
	kept := make(map[int]bool, len(tails))
	if len(tails) > 0 {
		for i := tails[len(tails)-1]; i >= 0; i = prev[i] {
			kept[i] = true
		}
	}
	var out []string
	for i, v := range newOrder {
		if !kept[i] {
			out = append(out, v)
		}
	}
	return out
}
