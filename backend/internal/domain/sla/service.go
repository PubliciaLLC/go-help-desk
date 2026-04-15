package sla

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/ticket"
)

// Service evaluates SLA policies against tickets.
type Service struct{ store Store }

// NewService returns a Service backed by the given Store.
func NewService(store Store) *Service { return &Service{store: store} }

// AttachPolicy finds the best matching SLA policy for a ticket and creates an
// SLA record for it. Called when a new ticket is created.
func (s *Service) AttachPolicy(ctx context.Context, t ticket.Ticket) error {
	policy, err := s.store.FindPolicy(ctx, t.Priority, t.CategoryID)
	if err != nil {
		return fmt.Errorf("finding SLA policy: %w", err)
	}
	if policy == nil {
		return nil // no policy configured for this priority/category
	}
	return s.store.CreateRecord(ctx, Record{
		TicketID: t.ID,
		PolicyID: policy.ID,
	})
}

// RecordFirstResponse marks the time of the first staff reply on a ticket.
// It is a no-op when already recorded.
func (s *Service) RecordFirstResponse(ctx context.Context, ticketID uuid.UUID, at time.Time) error {
	record, err := s.store.GetRecord(ctx, ticketID)
	if err != nil {
		return nil // no SLA record for this ticket
	}
	if record.FirstResponseAt != nil {
		return nil // already recorded
	}
	record.FirstResponseAt = &at
	return s.store.UpdateRecord(ctx, record)
}

// EvaluateBreaches checks whether a ticket has breached its SLA targets and
// stamps the breach timestamps if so. Called on a schedule.
func (s *Service) EvaluateBreaches(ctx context.Context, t ticket.Ticket, now time.Time) error {
	record, err := s.store.GetRecord(ctx, t.ID)
	if err != nil {
		return nil // no SLA record
	}
	policy, err := s.store.GetPolicy(ctx, record.PolicyID)
	if err != nil {
		return fmt.Errorf("getting SLA policy: %w", err)
	}

	changed := false
	if record.ResponseBreachedAt == nil && IsResponseBreached(record, policy, t.CreatedAt, now) {
		record.ResponseBreachedAt = &now
		changed = true
	}
	if record.ResolutionBreachedAt == nil && IsResolutionBreached(record, policy, t.CreatedAt, now) {
		record.ResolutionBreachedAt = &now
		changed = true
	}
	if changed {
		return s.store.UpdateRecord(ctx, record)
	}
	return nil
}

// ── Policy CRUD ───────────────────────────────────────────────────────────────

func (s *Service) CreatePolicy(ctx context.Context, p Policy) (Policy, error) {
	if p.Name == "" {
		return Policy{}, fmt.Errorf("policy name is required")
	}
	if p.ResponseTargetMin <= 0 {
		return Policy{}, fmt.Errorf("response target must be greater than zero")
	}
	if p.ResolutionTargetMin <= 0 {
		return Policy{}, fmt.Errorf("resolution target must be greater than zero")
	}
	p.ID = uuid.New()
	if err := s.store.CreatePolicy(ctx, p); err != nil {
		return Policy{}, fmt.Errorf("creating SLA policy: %w", err)
	}
	return p, nil
}

func (s *Service) GetPolicy(ctx context.Context, id uuid.UUID) (Policy, error) {
	return s.store.GetPolicy(ctx, id)
}

func (s *Service) UpdatePolicy(ctx context.Context, p Policy) error {
	if p.Name == "" {
		return fmt.Errorf("policy name is required")
	}
	if p.ResponseTargetMin <= 0 {
		return fmt.Errorf("response target must be greater than zero")
	}
	if p.ResolutionTargetMin <= 0 {
		return fmt.Errorf("resolution target must be greater than zero")
	}
	return s.store.UpdatePolicy(ctx, p)
}

func (s *Service) DeletePolicy(ctx context.Context, id uuid.UUID) error {
	return s.store.DeletePolicy(ctx, id)
}

func (s *Service) ListPolicies(ctx context.Context) ([]Policy, error) {
	return s.store.ListPolicies(ctx)
}
