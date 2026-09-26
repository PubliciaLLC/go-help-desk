package sla

import (
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// Color is the color band a live-computed SLA target renders as. See
// DESIGN.md "SLA Tracking (v1)" → "SLA Indicators".
type Color string

const (
	Green Color = "green"
	Amber Color = "amber"
	Red   Color = "red"
)

// TargetStatus is one target's (response or resolution) live read. Once MetAt
// is set, the numbers are frozen as of that instant and stop moving — the
// color is then the target's final color, per DESIGN.md "shows its final
// color".
type TargetStatus struct {
	Color        Color      `json:"color"`
	TargetMin    int        `json:"target_min"`
	ElapsedMin   int        `json:"elapsed_min"`   // floor of elapsed-toward-target
	RemainingMin int        `json:"remaining_min"` // target_min - elapsed_min; negative when over
	MetAt        *time.Time `json:"met_at"`        // first_response_at / resolved_at, or nil while outstanding
}

// Status is a ticket's live SLA read under the policy that governs its
// record: where each target stands right now, computed fresh on every read
// rather than derived from the breach stamp, which lags behind on the sweep
// interval.
type Status struct {
	PolicyID   uuid.UUID    `json:"policy_id"`
	PolicyName string       `json:"policy_name"`
	Response   TargetStatus `json:"response"`
	Resolution TargetStatus `json:"resolution"`
}

// StatusFor computes a ticket's live SLA status. now is used only for a
// target still outstanding; a target already met is judged as of its own met
// timestamp, so a late response stays red and an on-time one stays green
// forever after, however long ago it happened.
//
// The breach stamps on rec (ResponseBreachedAt / ResolutionBreachedAt) are
// deliberately never read here: the color is a live read of
// elapsed-toward-target, and the stamp exists for reporting and for freezing
// a fact in time (see EvaluateBreaches), not for driving the indicator. A
// test pins this so the stamp does not get wired back in and reintroduce the
// sweep-interval lag this type exists to avoid.
func StatusFor(rec Record, p Policy, t ticket.Ticket, now time.Time) Status {
	return Status{
		PolicyID:   p.ID,
		PolicyName: p.Name,
		Response:   targetStatus(rec.FirstResponseAt, rec.ResponseElapsedAtMetSeconds, p.ResponseTargetMin, t, now),
		Resolution: targetStatus(rec.ResolvedAt, rec.ResolutionElapsedAtMetSeconds, p.ResolutionTargetMin, t, now),
	}
}

// targetStatus computes one target's live read. Once metAt is set, elapsed
// must never move again, so a met target reads its FROZEN number
// (frozenSeconds, written by Service.RecordFirstResponse / RecordResolved at
// the instant it was met) rather than recomputing Elapsed(t, *metAt) against
// t — t.SLAPausedSeconds keeps growing for the rest of the ticket's life, so
// that recompute would silently subtract pause time that had not happened yet
// when the target was met (see Record's doc comment and CLAUDE.md).
//
// frozenSeconds is nil only for a record whose target was met before that
// column existed, or whose met instant migration 000028 could only estimate
// (no fact anywhere backed it, so nothing is frozen for it — see that
// migration's LIMITS section, #246); that case falls back to the old live
// recompute, which is wrong in exactly the way this function exists to fix,
// but only for rows a one-time migration backfill did not reach or could not
// exactly place.
func targetStatus(metAt *time.Time, frozenSeconds *int64, targetMin int, t ticket.Ticket, now time.Time) TargetStatus {
	target := time.Duration(targetMin) * time.Minute

	var elapsed time.Duration
	switch {
	case metAt == nil:
		elapsed = Elapsed(t, now)
	case frozenSeconds != nil:
		elapsed = time.Duration(*frozenSeconds) * time.Second
	default:
		elapsed = Elapsed(t, *metAt)
	}

	return TargetStatus{
		Color:        colorFor(elapsed, target),
		TargetMin:    targetMin,
		ElapsedMin:   int(elapsed / time.Minute),
		RemainingMin: int((target - elapsed) / time.Minute),
		MetAt:        metAt,
	}
}

// colorFor is the whole 80/100 rule. It works in time.Duration, comparing
// elapsed*5 against target*4 rather than dividing, so the 80% boundary is
// exact integer arithmetic and never subject to floating-point rounding.
func colorFor(elapsed, target time.Duration) Color {
	switch {
	case elapsed >= target:
		return Red // at or past 100%
	case elapsed*5 >= target*4:
		return Amber // at or past 80%
	default:
		return Green
	}
}
