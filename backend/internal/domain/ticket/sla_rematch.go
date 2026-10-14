package ticket

import (
	"time"

	"github.com/google/uuid"
)

// PriorityChangeReason says why a priority changed. It is recorded in the audit
// entry, so a reader can tell a person's edit from the reopen bump.
type PriorityChangeReason string

const (
	PriorityChangeManual   PriorityChangeReason = "manual"   // REST PATCH (and any later caller acting for a person)
	PriorityChangeReopened PriorityChangeReason = "reopened" // #464's first-reopen bump
)

// SLAOutcome is what a priority change did to the ticket's SLA. It is recorded
// in the audit entry, so the feed says whether the clock moved.
type SLAOutcome string

const (
	SLAOff       SLAOutcome = "sla_off"   // feature switched off: nothing re-matched
	SLANotOpen   SLAOutcome = "not_open"  // ticket Resolved or Closed: the clock is stopped, record untouched
	SLANone      SLAOutcome = "none"      // no record and no policy matches
	SLAUnchanged SLAOutcome = "unchanged" // the new priority matches the record's own policy
	SLAAttached  SLAOutcome = "attached"  // no record before; one starts at the change
	SLARematched SLAOutcome = "rematched" // record was running: superseded, new one keeps its clock
	SLARestarted SLAOutcome = "restarted" // record was met or breached: superseded, new clock from the change
	SLARemoved   SLAOutcome = "removed"   // record superseded, no policy matches the new priority
)

// SLARecordState is a ticket's current SLA record as the re-match needs it,
// read FOR UPDATE inside the priority change's transaction. The ticket package
// cannot name sla.Record (sla imports ticket), and the re-match only needs to
// know which row is current, what policy it is under, where its clock starts
// and whether it is still running.
type SLARecordState struct {
	ID, PolicyID         uuid.UUID
	StartedAt            *time.Time
	PausedSecondsAtStart int64
	// Settled: any of first_response_at, resolved_at, response_breached_at or
	// resolution_breached_at is set. A settled record is history that a
	// priority change must not carry a clock over from.
	Settled bool
}

// SLARecordStart is the record a re-match starts in place of the one it
// supersedes. A nil StartedAt means the clock starts at the ticket's creation.
type SLARecordStart struct {
	ID, TicketID, PolicyID uuid.UUID
	StartedAt              *time.Time
	PausedSecondsAtStart   int64
}

// planSLARematch decides what a priority change does to the SLA, as a pure
// function of what the transaction read. It writes nothing: the caller
// supersedes the current record on rematched, restarted and removed, and
// starts the returned record when it is not nil.
//
// open is false when the ticket is Resolved or Closed: its clock is stopped,
// and restarting one on a ticket nobody is working would breach it while it
// sits there.
func planSLARematch(enabled, open bool, cur *SLARecordState, matched *uuid.UUID, t Ticket, now time.Time) (SLAOutcome, *SLARecordStart) {
	switch {
	case !enabled:
		return SLAOff, nil
	case !open:
		return SLANotOpen, nil
	}

	// A new clock, from the change.
	fresh := func(o SLAOutcome) (SLAOutcome, *SLARecordStart) {
		return o, &SLARecordStart{
			ID: uuid.New(), TicketID: t.ID, PolicyID: *matched,
			StartedAt: &now, PausedSecondsAtStart: pausedSecondsAt(t, now),
		}
	}

	switch {
	case cur == nil && matched == nil:
		return SLANone, nil
	case cur == nil:
		return fresh(SLAAttached)
	case matched == nil:
		return SLARemoved, nil
	case cur.PolicyID == *matched:
		return SLAUnchanged, nil
	case cur.Settled:
		return fresh(SLARestarted)
	}
	// Still running under another policy: the new record keeps the clock, so no
	// elapsed time is lost.
	return SLARematched, &SLARecordStart{
		ID: uuid.New(), TicketID: t.ID, PolicyID: *matched,
		StartedAt: cur.StartedAt, PausedSecondsAtStart: cur.PausedSecondsAtStart,
	}
}

// pausedSecondsAt is the ticket's pause total as of at: every closed Pending
// interval, plus the part of an open one already elapsed. A restarted SLA
// record stores it as its base (paused_seconds_at_start), so pause time before
// the restart does not count toward the new record. The open interval is
// clipped to at, the way sla.Elapsed clips it.
func pausedSecondsAt(t Ticket, at time.Time) int64 {
	n := t.SLAPausedSeconds
	if t.PendingSince != nil && at.After(*t.PendingSince) {
		n += int64(at.Sub(*t.PendingSince) / time.Second)
	}
	return n
}
