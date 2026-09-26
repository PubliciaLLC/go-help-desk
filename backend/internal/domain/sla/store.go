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

	// SetFirstResponse marks ticketID's first response and freezes its
	// elapsed-toward-target reading as of that same moment, in one statement
	// that only ever writes first_response_at / response_elapsed_at_met_seconds
	// and only while they are still NULL (first writer wins) — the same
	// COALESCE-guarded, single-purpose shape as StampBreaches, and for the
	// same reason: a GetRecord-then-UpdateRecord round trip here could
	// silently overwrite a breach stamp StampBreaches wrote in between back to
	// NULL. Called for a ticket with no record is a silent no-op.
	SetFirstResponse(ctx context.Context, ticketID uuid.UUID, at time.Time, elapsedSeconds int64) error

	// SetResolved is SetFirstResponse's resolution-side twin.
	SetResolved(ctx context.Context, ticketID uuid.UUID, at time.Time, elapsedSeconds int64) error

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
	// neither met nor already stamped, and whose wall-clock age has passed
	// that target. It is a necessary but not sufficient prefilter — pausing
	// only ever subtracts from elapsed-toward-target, so a ticket younger
	// than its target by the wall clock cannot have breached under any
	// accounting, but one older than its target may still not have breached
	// once pause time is accounted for. The sufficient, pause-aware decision
	// is EvaluateBreaches' job.
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
