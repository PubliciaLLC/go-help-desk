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
	// ID identifies one record among the several a ticket can have once a
	// priority change has superseded one (#443b). Zero on a record that has
	// not been read from the store.
	ID                   uuid.UUID  `json:"id"`
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

	// StartedAt is where this record's clock starts. nil means the ticket's
	// creation, which is every record written before migration 000039 and
	// every record AttachPolicy writes. It is set only by a priority re-match
	// (#443b), which supersedes a record and starts a new one rather than
	// rewriting the old.
	StartedAt *time.Time `json:"started_at,omitempty"`

	// PausedSecondsAtStart is the ticket's pause total, plus the part of an
	// open Pending interval already elapsed, as of StartedAt. Pause time
	// before the restart does not count toward this record. Zero for a record
	// that starts at ticket creation.
	PausedSecondsAtStart int64 `json:"-"`

	// SupersededAt is set once, when a re-match replaces this record. The row
	// is then history: nothing reads it as the ticket's SLA and nothing writes
	// to it again.
	SupersededAt *time.Time `json:"superseded_at,omitempty"`
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
	return ElapsedFrom(t.CreatedAt, 0, t, at)
}

// ElapsedFrom is Elapsed with the clock starting at start and the ticket's
// pause total counted from pausedBase: at - start - (t.SLAPausedSeconds -
// pausedBase), less the open Pending interval. Elapsed(t, at) is
// ElapsedFrom(t.CreatedAt, 0, t, at).
//
// pausedBase includes the part of a Pending interval that was open at start.
// When that interval closes, SLAPausedSeconds grows by its whole length, so
// the subtraction comes out exactly right; while it is still open, the open
// interval (taken from its own start) cancels the base and the reading stays
// at zero.
//
// Like Elapsed, this is mirrored in SQL by ListSLABreachCandidates.
func ElapsedFrom(start time.Time, pausedBase int64, t ticket.Ticket, at time.Time) time.Duration {
	e := at.Sub(start) - time.Duration(t.SLAPausedSeconds-pausedBase)*time.Second
	if t.PendingSince != nil && at.After(*t.PendingSince) {
		e -= at.Sub(*t.PendingSince)
	}
	return e
}

// Elapsed is the record's own elapsed time: from StartedAt (or the ticket's
// creation when nil) and PausedSecondsAtStart. A record with no StartedAt
// reads exactly as the package-level Elapsed does.
func (r Record) Elapsed(t ticket.Ticket, at time.Time) time.Duration {
	start := t.CreatedAt
	if r.StartedAt != nil {
		start = *r.StartedAt
	}
	return ElapsedFrom(start, r.PausedSecondsAtStart, t, at)
}

// IsResponseBreached returns true when the response target has elapsed and no
// first response has been recorded.
func IsResponseBreached(r Record, p Policy, t ticket.Ticket, now time.Time) bool {
	if r.FirstResponseAt != nil {
		return false // already responded
	}
	return r.Elapsed(t, now) > time.Duration(p.ResponseTargetMin)*time.Minute
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
		return r.Elapsed(t, *r.ResolvedAt) > target
	}
	return r.Elapsed(t, now) > target
}
