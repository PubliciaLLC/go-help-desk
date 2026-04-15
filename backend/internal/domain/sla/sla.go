package sla

import (
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// Policy defines the response and resolution time targets for a given
// priority level, optionally narrowed to a specific category.
type Policy struct {
	ID                  uuid.UUID
	Name                string
	Priority            ticket.Priority
	CategoryID          *uuid.UUID // nil = applies to all categories
	ResponseTargetMin   int        // minutes until first response required
	ResolutionTargetMin int        // minutes until resolution required
}

// Record tracks SLA state for a single ticket.
type Record struct {
	TicketID             uuid.UUID
	PolicyID             uuid.UUID
	FirstResponseAt      *time.Time
	ResolvedAt           *time.Time
	ResponseBreachedAt   *time.Time
	ResolutionBreachedAt *time.Time
}

// IsResponseBreached returns true when the response target has elapsed and no
// first response has been recorded.
func IsResponseBreached(r Record, p Policy, ticketCreatedAt, now time.Time) bool {
	if r.FirstResponseAt != nil {
		return false // already responded
	}
	deadline := ticketCreatedAt.Add(time.Duration(p.ResponseTargetMin) * time.Minute)
	return now.After(deadline)
}

// IsResolutionBreached returns true when the resolution target has elapsed and
// the ticket has not been resolved.
func IsResolutionBreached(r Record, p Policy, ticketCreatedAt, now time.Time) bool {
	if r.ResolvedAt != nil {
		return false // already resolved
	}
	deadline := ticketCreatedAt.Add(time.Duration(p.ResolutionTargetMin) * time.Minute)
	return now.After(deadline)
}
