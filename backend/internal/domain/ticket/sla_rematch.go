package ticket

import (
	"time"

	"github.com/google/uuid"
)

// PriorityChangeReason says why a priority changed. It is recorded in the audit
// entry, and it decides what the change does to the SLA: a staff change never
// restarts a clock, and a reopen always does.
type PriorityChangeReason string

const (
	PriorityChangeManual   PriorityChangeReason = "manual"   // REST PATCH (and any later caller acting for a person)
	PriorityChangeReopened PriorityChangeReason = "reopened" // #464's first-reopen bump: restarts both clocks
)

// SLAOutcome is what a priority change did to the ticket's SLA. It is recorded
// in the audit entry, so the feed says whether the clock moved.
type SLAOutcome string

const (
	SLAOff       SLAOutcome = "sla_off"   // feature switched off: nothing re-matched
	SLANone      SLAOutcome = "none"      // no record and no policy matches
	SLAUnchanged SLAOutcome = "unchanged" // the new priority matches the record's own policy
	SLAAttached  SLAOutcome = "attached"  // no record before; one starts (a staff change: from ticket creation; a reopen: from now)
	SLARematched SLAOutcome = "rematched" // record superseded; the new one keeps its clock and its met targets
	SLARestarted SLAOutcome = "restarted" // reopen only: record superseded, both clocks start over from now
	SLARemoved   SLAOutcome = "removed"   // record superseded, no policy matches the new priority
)

// SLAFacts are what a record has recorded about what happened: when each
// target was met, the elapsed reading frozen at that instant, and the breach
// stamps. A re-match carries them onto the record that replaces it, so a
// target already met stays met.
type SLAFacts struct {
	FirstResponseAt               *time.Time
	ResolvedAt                    *time.Time
	ResponseBreachedAt            *time.Time
	ResolutionBreachedAt          *time.Time
	ResponseElapsedAtMetSeconds   *int64
	ResolutionElapsedAtMetSeconds *int64
}

// SLARecordState is a ticket's current SLA record as the re-match needs it,
// read FOR UPDATE inside the priority change's transaction. The ticket package
// cannot name sla.Record (sla imports ticket), and the re-match only needs to
// know which row is current, what policy it is under, where its clock starts
// and what it has recorded.
type SLARecordState struct {
	ID, PolicyID         uuid.UUID
	StartedAt            *time.Time
	PausedSecondsAtStart int64
	Facts                SLAFacts
}

// SLARecordStart is the record a re-match starts in place of the one it
// supersedes. A nil StartedAt means the clock starts at the ticket's creation.
type SLARecordStart struct {
	ID, TicketID, PolicyID uuid.UUID
	StartedAt              *time.Time
	PausedSecondsAtStart   int64
	Facts                  SLAFacts
}

// planSLARematch decides what a STAFF priority change does to the SLA, as a pure
// function of what the transaction read. It writes nothing: the caller
// supersedes the current record on rematched and removed, and starts the
// returned record when it is not nil.
//
// A staff change never restarts a clock. The new record keeps the old one's
// start, pause base and facts, so elapsed time still counts and a target
// already met stays met; only the policy changes. A ticket with no record
// whose new priority matches a policy gets one whose clock started at the
// ticket's creation (a nil StartedAt, read exactly as every legacy record is),
// not at the change: it may be breached at once.
//
// Resolved and Closed tickets never get here: ChangePriority refuses them.
func planSLARematch(enabled bool, cur *SLARecordState, matched *uuid.UUID, t Ticket, now time.Time) (SLAOutcome, *SLARecordStart) {
	switch {
	case !enabled:
		return SLAOff, nil
	case cur == nil && matched == nil:
		return SLANone, nil
	case cur == nil:
		return SLAAttached, &SLARecordStart{ID: uuid.New(), TicketID: t.ID, PolicyID: *matched}
	case matched == nil:
		return SLARemoved, nil
	case cur.PolicyID == *matched:
		return SLAUnchanged, nil
	}
	return SLARematched, &SLARecordStart{
		ID: uuid.New(), TicketID: t.ID, PolicyID: *matched,
		StartedAt: cur.StartedAt, PausedSecondsAtStart: cur.PausedSecondsAtStart, Facts: cur.Facts,
	}
}

// planSLARestart decides what a REOPEN does to the SLA (PriorityChangeReopened,
// called from inside the reopening reply's transaction): both clocks start over
// from now, whether or not the old record was met, and whether or not the
// priority, or the policy, changed. The old record is superseded and kept; the
// new one has no facts. With the feature switched off no record is created.
func planSLARestart(enabled bool, cur *SLARecordState, matched *uuid.UUID, t Ticket, now time.Time) (SLAOutcome, *SLARecordStart) {
	switch {
	case !enabled:
		return SLAOff, nil
	case matched == nil && cur == nil:
		return SLANone, nil
	case matched == nil:
		return SLARemoved, nil
	}
	outcome := SLARestarted
	if cur == nil {
		outcome = SLAAttached
	}
	return outcome, &SLARecordStart{
		ID: uuid.New(), TicketID: t.ID, PolicyID: *matched,
		StartedAt: &now, PausedSecondsAtStart: pausedSecondsAt(t, now),
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
