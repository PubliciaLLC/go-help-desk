package sla

import (
	"context"
	"errors"

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

	// FindPolicy returns the most specific policy for a ticket's priority and
	// category. Category-specific policies take precedence over global ones.
	FindPolicy(ctx context.Context, priority ticket.Priority, categoryID uuid.UUID) (*Policy, error)

	// Records
	CreateRecord(ctx context.Context, r Record) error
	GetRecord(ctx context.Context, ticketID uuid.UUID) (Record, error)
	UpdateRecord(ctx context.Context, r Record) error
}
