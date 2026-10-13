package notification

import (
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
	Invert      bool        `json:"invert"`
}

// IsEmpty reports whether f has no conditions. Invert is not a condition.
func (f WebhookFilter) IsEmpty() bool {
	return len(f.CategoryIDs) == 0 && len(f.Priorities) == 0 && len(f.GroupIDs) == 0
}

// Admits reports whether a ticket with attributes a passes f. known is false
// only when f has conditions and a is nil. Admits then returns admit=true:
// the caller delivers and logs (fail open).
func (f WebhookFilter) Admits(a *TicketAttrs) (admit, known bool) {
	if f.IsEmpty() {
		return true, true
	}
	if a == nil {
		return true, false
	}
	m := in(f.CategoryIDs, a.CategoryID) && in(f.Priorities, a.Priority) && inOpt(f.GroupIDs, a.AssigneeGroupID)
	return m != f.Invert, true
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
