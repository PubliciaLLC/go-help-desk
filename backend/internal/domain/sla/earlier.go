package sla

import (
	"time"

	"github.com/google/uuid"
)

// EarlierOutcome is how a superseded record stood when a priority change
// replaced it. Running is never reported: see EarlierRecord.
type EarlierOutcome string

const (
	EarlierMet      EarlierOutcome = "met"      // a target was met and nothing breached
	EarlierBreached EarlierOutcome = "breached" // a breach was stamped on it
)

// EarlierView is the record a ticket's current SLA record replaced, for the
// ticket page's "SLA restarted" badge to show (#443, Erik 2026-10-13: when the
// SLA was already met or breached, the priority change starts a new record and
// the page links to the earlier one). The facts only; the record itself stays
// where it is.
type EarlierView struct {
	PolicyID     uuid.UUID      `json:"policy_id"`
	PolicyName   string         `json:"policy_name"`
	StartedAt    time.Time      `json:"started_at"` // the ticket's creation when the record never restarted
	SupersededAt time.Time      `json:"superseded_at"`
	Outcome      EarlierOutcome `json:"outcome"`
}

// EarlierRecord picks the record to show from a ticket's records, newest clock
// first (Store.ListRecordsForTicket): the first superseded one. It is nil when
// there is none, and nil when that record was superseded with nothing met and
// nothing breached, because a clock that was still running when the priority
// changed carried on in the new record; there is nothing to say restarted.
// created is the ticket's creation, the start of a record whose started_at is
// empty. PolicyName is left for the caller, which has the policies.
//
// The outcome reads the record's stamps, not a live clock: a breach the sweep
// has not stamped yet is not reported.
func EarlierRecord(history []Record, created time.Time) *EarlierView {
	for _, r := range history {
		if r.SupersededAt == nil {
			continue
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
		start := created
		if r.StartedAt != nil {
			start = *r.StartedAt
		}
		return &EarlierView{PolicyID: r.PolicyID, StartedAt: start, SupersededAt: *r.SupersededAt, Outcome: outcome}
	}
	return nil
}
