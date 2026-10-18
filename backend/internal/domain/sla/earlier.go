package sla

import (
	"time"

	"github.com/google/uuid"
)

// EarlierOutcome is how a superseded record stood when a restart replaced it.
// Running is never reported: see EarlierRecord.
type EarlierOutcome string

const (
	EarlierMet      EarlierOutcome = "met"      // a target was met and nothing breached
	EarlierBreached EarlierOutcome = "breached" // a breach was stamped on it
)

// EarlierView is the record a restart replaced, for the ticket page's "SLA
// restarted" badge to show (#443, Erik 2026-10-14: only when the clock really
// restarted, which is a reopen; never after a staff or admin priority change).
// The facts only; the record itself stays where it is.
type EarlierView struct {
	PolicyID     uuid.UUID      `json:"policy_id"`
	PolicyName   string         `json:"policy_name"`
	StartedAt    time.Time      `json:"started_at"` // the ticket's creation when the record never restarted
	SupersededAt time.Time      `json:"superseded_at"`
	Outcome      EarlierOutcome `json:"outcome"`
}

// EarlierRecord picks the record to show from a ticket's records, newest clock
// first (Store.ListRecordsForTicket). It is nil unless the clock restarted.
//
// What tells a restart from a re-match is the clock start, not the stamps. A
// restart (a reopen) writes a started_at of its own; a re-match copies the old
// record's started_at, empty or not, along with its met and breach stamps, so
// those stamps say nothing about which of the two made the record (reading them
// is what made the badge appear after any priority change and never go away).
// So: the current record's clock start is the reference. A clock that starts at
// the ticket's creation (started_at empty) cannot have restarted. Otherwise the
// first superseded record whose clock started somewhere else is the last record
// of the clock the restart replaced; records that share the current start are
// re-matches of it and are passed over, which is why a re-match after a restart
// still reports the restart.
//
// It is also nil when that record ended with nothing met and nothing breached,
// because there is then no outcome to show. created is the ticket's creation,
// the start of a record whose started_at is empty. PolicyName is left for the
// caller, which has the policies.
//
// The outcome reads the record's stamps, not a live clock: a breach the sweep
// has not stamped yet is not reported.
func EarlierRecord(history []Record, created time.Time) *EarlierView {
	var current *Record
	for i := range history {
		if history[i].SupersededAt == nil {
			current = &history[i]
			break
		}
	}
	if current == nil || current.StartedAt == nil {
		return nil
	}
	for _, r := range history {
		if r.SupersededAt == nil {
			continue
		}
		start := created
		if r.StartedAt != nil {
			start = *r.StartedAt
		}
		if start.Equal(*current.StartedAt) {
			continue // a re-match of the current clock
		}
		var outcome EarlierOutcome
		switch {
		case r.ResponseBreachedAt != nil || r.ResolutionBreachedAt != nil:
			outcome = EarlierBreached
		case r.FirstResponseAt != nil || r.ResolvedAt != nil:
			outcome = EarlierMet
		default:
			return nil
		}
		return &EarlierView{PolicyID: r.PolicyID, StartedAt: start, SupersededAt: *r.SupersededAt, Outcome: outcome}
	}
	return nil
}
