package notification

import (
	"encoding/json"
	"slices"

	"github.com/google/uuid"
)

// WebhookFilter narrows which tickets a webhook fires for (#413). Each list is
// one condition, and an empty list is no condition. A list matches when the
// ticket's value is any of its entries. Every condition must match. Invert
// fires for the tickets that do NOT match. The zero value has no conditions
// and admits every ticket: that is "no filter", so there is no nil case.
type WebhookFilter struct {
	CategoryIDs []uuid.UUID `json:"category_ids"`
	Priorities  []string    `json:"priorities"`
	GroupIDs    []uuid.UUID `json:"group_ids"`
	// TypeIDs and ItemIDs (#631) narrow by the ticket's type and item, and
	// combine with the categories by AND like every other condition. They are
	// omitted from the JSON while empty so a filter that does not use them reads
	// as it did before they existed; an older binary reading a filter that does
	// use them ignores the keys, which widens the hook.
	TypeIDs []uuid.UUID `json:"type_ids,omitempty"`
	ItemIDs []uuid.UUID `json:"item_ids,omitempty"`
	Invert  bool        `json:"invert"`
}

// IsEmpty reports whether f has no conditions. Invert is not a condition.
func (f WebhookFilter) IsEmpty() bool {
	// Every list must be counted: one left out makes a filter that has only that
	// condition read as "no filter", and the hook fires for every ticket.
	return len(f.CategoryIDs) == 0 && len(f.Priorities) == 0 && len(f.GroupIDs) == 0 &&
		len(f.TypeIDs) == 0 && len(f.ItemIDs) == 0
}

// Admits reports whether a ticket with attributes a passes f. known is false
// only when f has conditions and a is nil. Admits then returns admit=true:
// the caller delivers and logs (fail open).
func (f WebhookFilter) Admits(a *TicketState) (admit, known bool) {
	if f.IsEmpty() {
		return true, true
	}
	if a == nil {
		return true, false
	}
	m := in(f.CategoryIDs, a.CategoryID) && in(f.Priorities, a.Priority) && inOpt(f.GroupIDs, a.AssigneeGroupID) &&
		inOpt(f.TypeIDs, a.TypeID) && inOpt(f.ItemIDs, a.ItemID)
	return m != f.Invert, true
}

// MarshalJSON writes nil lists as [] so the API never sends null for them: the
// admin page maps over them.
func (f WebhookFilter) MarshalJSON() ([]byte, error) {
	type plain WebhookFilter
	p := plain(f)
	if p.CategoryIDs == nil {
		p.CategoryIDs = []uuid.UUID{}
	}
	if p.Priorities == nil {
		p.Priorities = []string{}
	}
	if p.GroupIDs == nil {
		p.GroupIDs = []uuid.UUID{}
	}
	return json.Marshal(p)
}

// in reports whether v is admitted by one condition: an empty list is no
// condition, so it admits everything.
func in[T comparable](list []T, v T) bool {
	return len(list) == 0 || slices.Contains(list, v)
}

// inOpt is in for a value that may be absent. An absent value is never in a
// non-empty list.
func inOpt(list []uuid.UUID, v *uuid.UUID) bool {
	return len(list) == 0 || (v != nil && slices.Contains(list, *v))
}
