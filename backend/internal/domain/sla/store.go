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
	UpdateRecord(ctx context.Context, r Record) error

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
