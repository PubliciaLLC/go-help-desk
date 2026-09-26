package sla

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// ErrNoRecord reports that a ticket has no SLA record.
//
// It exists so the service can tell "this ticket is not under an SLA" from
// "the database is unreachable". Without it both arrived as an opaque error,
// the service treated every failure as the former, and a transient outage
// silently became a permanently missing first-response timestamp — which later
// reads as a genuine breach.
var ErrNoRecord = errors.New("no SLA record for ticket")

// Store is the persistence interface for SLA policies and records.
type Store interface {
	// Policies
	CreatePolicy(ctx context.Context, p Policy) error
	GetPolicy(ctx context.Context, id uuid.UUID) (Policy, error)
	UpdatePolicy(ctx context.Context, p Policy) error
	DeletePolicy(ctx context.Context, id uuid.UUID) error
	ListPolicies(ctx context.Context) ([]Policy, error)

	// FindPolicy returns the most specific policy matching a ticket's priority
	// and category, in the order DESIGN.md documents: priority+category, then
	// priority only, then category only, then the catch-all. A policy field
	// left nil matches any value. nil, nil means no policy applies.
	FindPolicy(ctx context.Context, priority ticket.Priority, categoryID uuid.UUID) (*Policy, error)

	// Records
	CreateRecord(ctx context.Context, r Record) error
	GetRecord(ctx context.Context, ticketID uuid.UUID) (Record, error)
	// UpdateRecord is a full-row read-modify-write. RecordFirstResponse and
	// RecordResolved do not use it (see SetFirstResponse / SetResolved below);
	// nothing in this codebase calls it any more, and nothing should start:
	// see the comment on StampBreaches for why a full-row write is unsafe
	// against a concurrent breach stamp.
	UpdateRecord(ctx context.Context, r Record) error

	// SetFirstResponse marks ticketID's first response, freezes its
	// elapsed-toward-target reading as of that same moment, AND stamps a
	// response breach if it is already late — all in one statement that only
	// ever writes first_response_at / response_elapsed_at_met_seconds /
	// response_breached_at, and only while each is still NULL (first writer
	// wins). Folding the breach decision into the same write as the fact
	// (#228) means it is made from THIS call's own elapsed reading landing
	// atomically with the fact, never from a separately-read, possibly-stale
	// record — a losing call (one that finds first_response_at already set)
	// touches nothing, including the breach column, so it can never stamp a
	// breach the winning call did not itself decide. Called for a ticket with
	// no record is a silent no-op.
	SetFirstResponse(ctx context.Context, ticketID uuid.UUID, at time.Time, elapsedSeconds, responseTargetSeconds int64) error

	// SetResolved is SetFirstResponse's resolution-side twin.
	//
	// Not called by Service.RecordResolved any more (#233): see
	// SetResolvedAndFirstResponse below for why that door needs both writes
	// folded into one statement. Kept as its own primitive because it is
	// still exercised directly (and independently regression-tested) at the
	// store level; nothing stops a future caller that only ever needs the
	// resolution fact alone from using it.
	SetResolved(ctx context.Context, ticketID uuid.UUID, at time.Time, elapsedSeconds, resolutionTargetSeconds int64) error

	// SetResolvedAndFirstResponse is what Service.RecordResolved actually
	// calls: SetResolved and SetFirstResponse's own writes (the #219 "a
	// resolution is a response too" backfill), folded into ONE statement.
	//
	// #233: RecordResolved used to call SetResolved then SetFirstResponse as
	// two separate statements. A failure between them left resolved_at set
	// and first_response_at permanently NULL — RecordResolved's own fast
	// no-op guard (resolved_at already set) means nothing ever retries the
	// second write, and the sweep's response branch (first_response_at IS
	// NULL) keeps selecting the ticket and eventually stamps a PERMANENT
	// false response breach. One statement removes the gap entirely: both
	// facts, and both of their own independently-decided breach stamps,
	// commit together or not at all. Each pair's breach CASE reads only its
	// own pre-image column, so a ticket that already has a genuine EARLIER
	// first_response_at is left with that fact — and whatever breach
	// decision was already made for it — completely untouched; only the
	// resolution pair is written.
	SetResolvedAndFirstResponse(ctx context.Context, ticketID uuid.UUID, at time.Time, elapsedSeconds, resolutionTargetSeconds, responseTargetSeconds int64) error

	// ListRecordsByTicketIDs returns the SLA records for whichever of the
	// given ticket ids have one. A ticket with no record is simply absent
	// from the result — that is "not under an SLA", the same thing GetRecord
	// reports as ErrNoRecord for one ticket at a time. Order is unspecified;
	// callers index by TicketID. Used to attach live SLA status to a page of
	// tickets in one query instead of one GetRecord per row (see
	// Service.StatusesFor).
	ListRecordsByTicketIDs(ctx context.Context, ticketIDs []uuid.UUID) ([]Record, error)

	// ListBreachCandidates returns the ids of tickets the breach sweep must
	// evaluate: open, under a policy, with at least one target that is
	// neither met nor already stamped. The prefilter uses the same
	// pause-aware elapsed time as sla.Elapsed, so a ticket parked in Pending
	// whose frozen elapsed time is still under target is never selected. It
	// still returns a superset, not an exact answer: Go makes the final call
	// at the equality instant, from a fresh read of the row.
	ListBreachCandidates(ctx context.Context, now time.Time) ([]uuid.UUID, error)

	// StampBreaches sets response and/or resolution as the record's breach
	// timestamps for ticketID, but never overwrites a column that is already
	// set (first writer wins) and never touches any other column — unlike
	// UpdateRecord, which is a full-row read-modify-write and so is not safe
	// to use for a stamp decided from a possibly-stale read. A nil argument
	// leaves that column untouched. Stamping a ticket with no record is a
	// silent no-op.
	StampBreaches(ctx context.Context, ticketID uuid.UUID, response, resolution *time.Time) error
}
