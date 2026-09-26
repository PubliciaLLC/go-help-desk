// Package slastore implements domain/sla.Store against PostgreSQL.
package slastore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// Store implements sla.Store.
type Store struct{ q *dbgen.Queries }

// New returns a Store backed by the given Queries.
func New(q *dbgen.Queries) *Store { return &Store{q: q} }

func (s *Store) CreatePolicy(ctx context.Context, p sla.Policy) error {
	return s.q.CreateSLAPolicy(ctx, dbgen.CreateSLAPolicyParams{
		ID:                  p.ID,
		Name:                p.Name,
		Priority:            nullPriority(p.Priority),
		CategoryID:          database.NullUUID(p.CategoryID),
		ResponseTargetMin:   int32(p.ResponseTargetMin),
		ResolutionTargetMin: int32(p.ResolutionTargetMin),
	})
}

func (s *Store) GetPolicy(ctx context.Context, id uuid.UUID) (sla.Policy, error) {
	r, err := s.q.GetSLAPolicy(ctx, id)
	if err != nil {
		return sla.Policy{}, fmt.Errorf("getting SLA policy %s: %w", id, err)
	}
	return policyFromRow(r), nil
}

func (s *Store) UpdatePolicy(ctx context.Context, p sla.Policy) error {
	return s.q.UpdateSLAPolicy(ctx, dbgen.UpdateSLAPolicyParams{
		ID:                  p.ID,
		Name:                p.Name,
		Priority:            nullPriority(p.Priority),
		CategoryID:          database.NullUUID(p.CategoryID),
		ResponseTargetMin:   int32(p.ResponseTargetMin),
		ResolutionTargetMin: int32(p.ResolutionTargetMin),
	})
}

func (s *Store) DeletePolicy(ctx context.Context, id uuid.UUID) error {
	return s.q.DeleteSLAPolicy(ctx, id)
}

func (s *Store) ListPolicies(ctx context.Context) ([]sla.Policy, error) {
	rows, err := s.q.ListSLAPolicies(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing SLA policies: %w", err)
	}
	out := make([]sla.Policy, len(rows))
	for i, r := range rows {
		out[i] = policyFromRow(r)
	}
	return out, nil
}

func (s *Store) FindPolicy(ctx context.Context, priority ticket.Priority, categoryID uuid.UUID) (*sla.Policy, error) {
	r, err := s.q.FindSLAPolicy(ctx, dbgen.FindSLAPolicyParams{
		Priority:   string(priority),
		CategoryID: categoryID,
	})
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("finding SLA policy: %w", err)
	}
	p := policyFromRow(r)
	return &p, nil
}

func (s *Store) CreateRecord(ctx context.Context, r sla.Record) error {
	return s.q.CreateSLARecord(ctx, dbgen.CreateSLARecordParams{
		TicketID:             r.TicketID,
		PolicyID:             r.PolicyID,
		FirstResponseAt:      database.NullTime(r.FirstResponseAt),
		ResolvedAt:           database.NullTime(r.ResolvedAt),
		ResponseBreachedAt:   database.NullTime(r.ResponseBreachedAt),
		ResolutionBreachedAt: database.NullTime(r.ResolutionBreachedAt),
	})
}

func (s *Store) GetRecord(ctx context.Context, ticketID uuid.UUID) (sla.Record, error) {
	r, err := s.q.GetSLARecord(ctx, ticketID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sla.Record{}, fmt.Errorf("%w: %s", sla.ErrNoRecord, ticketID)
		}
		return sla.Record{}, fmt.Errorf("getting SLA record %s: %w", ticketID, err)
	}
	return recordFromRow(r), nil
}

func (s *Store) UpdateRecord(ctx context.Context, r sla.Record) error {
	return s.q.UpdateSLARecord(ctx, dbgen.UpdateSLARecordParams{
		TicketID:             r.TicketID,
		FirstResponseAt:      database.NullTime(r.FirstResponseAt),
		ResolvedAt:           database.NullTime(r.ResolvedAt),
		ResponseBreachedAt:   database.NullTime(r.ResponseBreachedAt),
		ResolutionBreachedAt: database.NullTime(r.ResolutionBreachedAt),
	})
}

func (s *Store) SetFirstResponse(ctx context.Context, ticketID uuid.UUID, at time.Time, elapsedSeconds, responseTargetSeconds int64) error {
	if err := s.q.SetSLAFirstResponse(ctx, dbgen.SetSLAFirstResponseParams{
		TicketID:              ticketID,
		At:                    at,
		ElapsedSeconds:        elapsedSeconds,
		ResponseTargetSeconds: responseTargetSeconds,
	}); err != nil {
		return fmt.Errorf("setting SLA first response for ticket %s: %w", ticketID, err)
	}
	return nil
}

func (s *Store) SetResolved(ctx context.Context, ticketID uuid.UUID, at time.Time, elapsedSeconds, resolutionTargetSeconds int64) error {
	if err := s.q.SetSLAResolved(ctx, dbgen.SetSLAResolvedParams{
		TicketID:                ticketID,
		At:                      at,
		ElapsedSeconds:          elapsedSeconds,
		ResolutionTargetSeconds: resolutionTargetSeconds,
	}); err != nil {
		return fmt.Errorf("setting SLA resolution for ticket %s: %w", ticketID, err)
	}
	return nil
}

func (s *Store) ListRecordsByTicketIDs(ctx context.Context, ticketIDs []uuid.UUID) ([]sla.Record, error) {
	rows, err := s.q.ListSLARecordsByTicketIDs(ctx, ticketIDs)
	if err != nil {
		return nil, fmt.Errorf("listing SLA records for tickets: %w", err)
	}
	out := make([]sla.Record, len(rows))
	for i, r := range rows {
		out[i] = recordFromRow(r)
	}
	return out, nil
}

func recordFromRow(r dbgen.SlaRecord) sla.Record {
	return sla.Record{
		TicketID:                      r.TicketID,
		PolicyID:                      r.PolicyID,
		FirstResponseAt:               database.TimePtr(r.FirstResponseAt),
		ResolvedAt:                    database.TimePtr(r.ResolvedAt),
		ResponseBreachedAt:            database.TimePtr(r.ResponseBreachedAt),
		ResolutionBreachedAt:          database.TimePtr(r.ResolutionBreachedAt),
		ResponseElapsedAtMetSeconds:   database.Int64Ptr(r.ResponseElapsedAtMetSeconds),
		ResolutionElapsedAtMetSeconds: database.Int64Ptr(r.ResolutionElapsedAtMetSeconds),
	}
}

func (s *Store) ListBreachCandidates(ctx context.Context, now time.Time) ([]uuid.UUID, error) {
	ids, err := s.q.ListSLABreachCandidates(ctx, now)
	if err != nil {
		return nil, fmt.Errorf("listing SLA breach candidates: %w", err)
	}
	return ids, nil
}

func (s *Store) StampBreaches(ctx context.Context, ticketID uuid.UUID, response, resolution *time.Time) error {
	if err := s.q.StampSLABreaches(ctx, dbgen.StampSLABreachesParams{
		TicketID:             ticketID,
		ResponseBreachedAt:   database.NullTime(response),
		ResolutionBreachedAt: database.NullTime(resolution),
	}); err != nil {
		return fmt.Errorf("stamping SLA breaches for ticket %s: %w", ticketID, err)
	}
	return nil
}

func policyFromRow(r dbgen.SlaPolicy) sla.Policy {
	return sla.Policy{
		ID:                  r.ID,
		Name:                r.Name,
		Priority:            priorityPtr(r.Priority),
		CategoryID:          database.UUIDPtr(r.CategoryID),
		ResponseTargetMin:   int(r.ResponseTargetMin),
		ResolutionTargetMin: int(r.ResolutionTargetMin),
	}
}

// ticket.Priority is a named string type, so it cannot travel through the
// shared *string helpers in package database without a conversion here.
// A NULL priority is the "any priority" tier.
func nullPriority(p *ticket.Priority) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*p), Valid: true}
}

func priorityPtr(n sql.NullString) *ticket.Priority {
	if !n.Valid {
		return nil
	}
	v := ticket.Priority(n.String)
	return &v
}
