package sla

import (
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// Policy defines the response and resolution time targets for tickets it
// matches, optionally narrowed to a priority, a category, or both. A policy
// that narrows neither is the catch-all tier.
type Policy struct {
	ID                  uuid.UUID        `json:"id"`
	Name                string           `json:"name"`
	Priority            *ticket.Priority `json:"priority,omitempty"`    // nil = applies to all priorities
	CategoryID          *uuid.UUID       `json:"category_id,omitempty"` // nil = applies to all categories
	ResponseTargetMin   int              `json:"response_target_min"`   // minutes until first response required
	ResolutionTargetMin int              `json:"resolution_target_min"` // minutes until resolution required
}

// Record tracks SLA state for a single ticket.
type Record struct {
	TicketID             uuid.UUID  `json:"ticket_id"`
	PolicyID             uuid.UUID  `json:"policy_id"`
	FirstResponseAt      *time.Time `json:"first_response_at,omitempty"`
	ResolvedAt           *time.Time `json:"resolved_at,omitempty"`
	ResponseBreachedAt   *time.Time `json:"response_breached_at,omitempty"`
	ResolutionBreachedAt *time.Time `json:"resolution_breached_at,omitempty"`
}

// Elapsed is the time a ticket has spent counting toward its targets as of at:
// wall-clock since creation, less every Pending interval that has closed,
// less the open one when the ticket is Pending at at.
//
// The open interval is clipped to at so a reading taken "as of" an instant
// before the ticket went Pending — the resolution time, re-read later — does
// not subtract time that had not been paused yet.
func Elapsed(t ticket.Ticket, at time.Time) time.Duration {
	e := at.Sub(t.CreatedAt) - time.Duration(t.SLAPausedSeconds)*time.Second
	if t.PendingSince != nil && at.After(*t.PendingSince) {
		e -= at.Sub(*t.PendingSince)
	}
	return e
}

// IsResponseBreached returns true when the response target has elapsed and no
// first response has been recorded.
func IsResponseBreached(r Record, p Policy, t ticket.Ticket, now time.Time) bool {
	if r.FirstResponseAt != nil {
		return false // already responded
	}
	return Elapsed(t, now) > time.Duration(p.ResponseTargetMin)*time.Minute
}

// IsResolutionBreached returns true when the resolution target has elapsed and
// the ticket has not been resolved.
func IsResolutionBreached(r Record, p Policy, t ticket.Ticket, now time.Time) bool {
	target := time.Duration(p.ResolutionTargetMin) * time.Minute
	if r.ResolvedAt != nil {
		// Resolved: judge it by WHEN, not by the clock now. Treating any
		// resolved ticket as met would hide every late resolution; the old
		// early return did that, and nothing recorded ResolvedAt anyway, so
		// on-time resolutions were about to be reported as breaches instead.
		return Elapsed(t, *r.ResolvedAt) > target
	}
	return Elapsed(t, now) > target
}
