package sla

import (
	"context"

	"github.com/google/uuid"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/ticket"
)

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
