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

	// ResponseElapsedAtMetSeconds / ResolutionElapsedAtMetSeconds are the
	// elapsed-toward-target NUMBER frozen at the instant FirstResponseAt /
	// ResolvedAt was recorded (see Service.RecordFirstResponse /
	// RecordResolved), not merely the timestamp it happened at.
	//
	// Elapsed(t, at) subtracts t.SLAPausedSeconds, which is a single
	// accumulated total that keeps growing for the rest of the ticket's life.
	// Recomputing Elapsed(t, *FirstResponseAt) against today's ticket row
	// would therefore subtract pause time that had not even happened yet when
	// the target was met, silently shrinking — or even flipping the color of
	// — a reading that is supposed to be final. targetStatus reads this
	// column instead, for exactly that reason.
	//
	// nil only for a record whose target was met before this column existed,
	// or whose met instant migration 000028 could only estimate (no fact
	// anywhere backed it, so no number is frozen — see that migration's
	// LIMITS section, #246); targetStatus falls back to the old
	// (reopenable-to-the-same-bug) live recompute for those, never for a
	// fresh RecordFirstResponse/RecordResolved.
	ResponseElapsedAtMetSeconds   *int64 `json:"response_elapsed_at_met_seconds,omitempty"`
	ResolutionElapsedAtMetSeconds *int64 `json:"resolution_elapsed_at_met_seconds,omitempty"`
}

// Elapsed is the time a ticket has spent counting toward its targets as of at:
// wall-clock since creation, less every Pending interval that has closed,
// less the open one when the ticket is Pending at at.
//
// The open interval is clipped to at so a reading taken "as of" an instant
// before the ticket went Pending — the resolution time, re-read later — does
// not subtract time that had not been paused yet.
//
// This formula is mirrored in SQL by ListSLABreachCandidates, and the two
// must change together: SQL elapsed must stay greater than or equal to Go
// elapsed (for example, if business hours are added later), so the query
// remains a superset of what EvaluateBreaches would stamp.
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
		//
		// Prefer the FROZEN elapsed reading over a live recompute, matching
		// how the indicator (status.go's targetStatus) already reads frozen
		// vs. live: recomputing Elapsed(t, *r.ResolvedAt) against the
		// ticket's CURRENT SLAPausedSeconds can shrink below target if the
		// ticket was reopened, paused, and released again after resolution —
		// which can only turn a true breach into a false negative, never the
		// reverse, but it means this and the frozen indicator could disagree
		// (#218). Falls back to the live recompute only for a record whose
		// target was met before this column existed (a one-time migration
		// backfill that did not reach every row), or whose met instant
		// migration 000028 could only estimate (#246) — see Record's doc
		// comment.
		if r.ResolutionElapsedAtMetSeconds != nil {
			return time.Duration(*r.ResolutionElapsedAtMetSeconds)*time.Second > target
		}
		return Elapsed(t, *r.ResolvedAt) > target
	}
	return Elapsed(t, now) > target
}
