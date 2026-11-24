package dashboard

import (
	"time"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// AttentionReason says why a ticket needs attention. Its value is the wire
// value the page reads.
type AttentionReason string

const (
	ReasonBreached   AttentionReason = "breached"
	ReasonAtRisk     AttentionReason = "at_risk"
	ReasonUnassigned AttentionReason = "unassigned"
	ReasonReopened   AttentionReason = "reopened"
)

// AttentionLimit is how many rows the needs-attention list shows.
const AttentionLimit = 5

// ReopenedWindow is how far back a reopened ticket still needs attention.
const ReopenedWindow = 7 * 24 * time.Hour

// AttentionList is the tickets one reason picked, already narrowed by the
// caller to what it may see.
type AttentionList struct {
	Reason  AttentionReason
	Tickets []ticket.Ticket
}

// AttentionItem is one row of the needs-attention list. Since is the instant
// its age is measured from, set by the reason.
type AttentionItem struct {
	Reason AttentionReason
	Since  time.Time
	Ticket ticket.Ticket
}

// PickAttention merges the lists in the order given. Each ticket is taken once,
// under the first reason that lists it, and the result stops at limit. It never
// returns nil, so an empty result encodes as an empty list.
func PickAttention(lists []AttentionList, limit int) []AttentionItem {
	out := []AttentionItem{}
	seen := map[uuid.UUID]bool{}
	for _, l := range lists {
		for _, t := range l.Tickets {
			if len(out) >= limit {
				return out
			}
			if seen[t.ID] {
				continue
			}
			seen[t.ID] = true
			out = append(out, AttentionItem{Reason: l.Reason, Since: since(l.Reason, t), Ticket: t})
		}
	}
	return out
}

// since is the instant a row's age is measured from. A reopened ticket is
// measured from its reopen; without one, from its creation.
func since(r AttentionReason, t ticket.Ticket) time.Time {
	if r == ReasonReopened && t.LastReopenedAt != nil {
		return *t.LastReopenedAt
	}
	return t.CreatedAt
}
