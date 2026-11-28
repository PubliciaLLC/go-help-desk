package ticketstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// The four ticket.Store methods a priority change's SLA re-match runs on
// (#443b). They live on the ticket store, not the SLA store, because the
// re-match runs inside the ticket's transaction and ticket.Atomic hands out
// only a ticket.Store and an audit.Store. They share the SLA queries.

// CurrentSLARecordForUpdate returns the ticket's current SLA record, locked
// until the transaction ends. nil, nil means the ticket has none.
func (s *Store) CurrentSLARecordForUpdate(ctx context.Context, ticketID uuid.UUID) (*ticket.SLARecordState, error) {
	r, err := s.q.GetCurrentSLARecordForUpdate(ctx, ticketID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading current SLA record of ticket %s: %w", ticketID, err)
	}
	return &ticket.SLARecordState{
		ID:                   r.ID,
		PolicyID:             r.PolicyID,
		StartedAt:            database.TimePtr(r.StartedAt),
		PausedSecondsAtStart: r.PausedSecondsAtStart,
		Facts: ticket.SLAFacts{
			FirstResponseAt:               database.TimePtr(r.FirstResponseAt),
			ResolvedAt:                    database.TimePtr(r.ResolvedAt),
			ResponseBreachedAt:            database.TimePtr(r.ResponseBreachedAt),
			ResolutionBreachedAt:          database.TimePtr(r.ResolutionBreachedAt),
			ResponseAtRiskAt:              database.TimePtr(r.ResponseAtRiskAt),
			ResolutionAtRiskAt:            database.TimePtr(r.ResolutionAtRiskAt),
			ResponseElapsedAtMetSeconds:   database.Int64Ptr(r.ResponseElapsedAtMetSeconds),
			ResolutionElapsedAtMetSeconds: database.Int64Ptr(r.ResolutionElapsedAtMetSeconds),
		},
	}, nil
}

// LatestSupersededSLARecord returns the record the ticket most recently stopped
// having. nil, nil means it never had one.
func (s *Store) LatestSupersededSLARecord(ctx context.Context, ticketID uuid.UUID) (*ticket.SLARecordState, error) {
	r, err := s.q.GetLatestSupersededSLARecord(ctx, ticketID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading latest superseded SLA record of ticket %s: %w", ticketID, err)
	}
	return &ticket.SLARecordState{
		ID:                   r.ID,
		PolicyID:             r.PolicyID,
		StartedAt:            database.TimePtr(r.StartedAt),
		PausedSecondsAtStart: r.PausedSecondsAtStart,
	}, nil
}

// MatchSLAPolicy returns the id of the policy that governs a ticket with this
// priority and category, by the same four tiers a new ticket is matched with.
// nil, nil means none does.
func (s *Store) MatchSLAPolicy(ctx context.Context, p ticket.Priority, categoryID uuid.UUID) (*uuid.UUID, error) {
	row, err := s.q.FindSLAPolicy(ctx, dbgen.FindSLAPolicyParams{Priority: string(p), CategoryID: categoryID})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("matching SLA policy: %w", err)
	}
	return &row.ID, nil
}

// SupersedeSLARecord marks a record as replaced. It writes superseded_at and
// nothing else: every fact on the row stays as it was. A record that is not
// current is an error, because the caller holds the row lock and has just read
// it as current.
func (s *Store) SupersedeSLARecord(ctx context.Context, id uuid.UUID, at time.Time) error {
	n, err := s.q.SupersedeSLARecord(ctx, dbgen.SupersedeSLARecordParams{ID: id, SupersededAt: at})
	if err != nil {
		return fmt.Errorf("superseding SLA record %s: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("superseding SLA record %s: it is not the current record", id)
	}
	return nil
}

// StartSLARecord inserts the record a re-match starts. The schema allows one
// current record per ticket, so this fails unless the previous one has been
// superseded first.
func (s *Store) StartSLARecord(ctx context.Context, r ticket.SLARecordStart) error {
	if err := s.q.InsertSLARecord(ctx, dbgen.InsertSLARecordParams{
		ID:                   r.ID,
		TicketID:             r.TicketID,
		PolicyID:             r.PolicyID,
		StartedAt:            database.NullTime(r.StartedAt),
		PausedSecondsAtStart: r.PausedSecondsAtStart,

		FirstResponseAt:               database.NullTime(r.Facts.FirstResponseAt),
		ResolvedAt:                    database.NullTime(r.Facts.ResolvedAt),
		ResponseBreachedAt:            database.NullTime(r.Facts.ResponseBreachedAt),
		ResolutionBreachedAt:          database.NullTime(r.Facts.ResolutionBreachedAt),
		ResponseAtRiskAt:              database.NullTime(r.Facts.ResponseAtRiskAt),
		ResolutionAtRiskAt:            database.NullTime(r.Facts.ResolutionAtRiskAt),
		ResponseElapsedAtMetSeconds:   database.NullInt64(r.Facts.ResponseElapsedAtMetSeconds),
		ResolutionElapsedAtMetSeconds: database.NullInt64(r.Facts.ResolutionElapsedAtMetSeconds),
	}); err != nil {
		return fmt.Errorf("starting SLA record for ticket %s: %w", r.TicketID, err)
	}
	return nil
}

// StampSLACrossings sets the stamp columns the sweep asks for and says which of
// them this statement newly set. See ticket.Store.StampSLACrossings.
func (s *Store) StampSLACrossings(ctx context.Context, recordID uuid.UUID, stamps ticket.SLAStampRequest) (ticket.SLAStamped, error) {
	row, err := s.q.StampSLACrossings(ctx, dbgen.StampSLACrossingsParams{
		ID:               recordID,
		At:               stamps.At,
		ResponseBreach:   stamps.ResponseBreach,
		ResolutionBreach: stamps.ResolutionBreach,
		ResponseAtRisk:   stamps.ResponseAtRisk,
		ResolutionAtRisk: stamps.ResolutionAtRisk,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ticket.SLAStamped{}, fmt.Errorf("stamping SLA crossings for record %s: %w", recordID, ticket.ErrSLARecordSuperseded)
		}
		return ticket.SLAStamped{}, fmt.Errorf("stamping SLA crossings for record %s: %w", recordID, err)
	}
	return ticket.SLAStamped{
		ResponseBreach:   row.ResponseBreach.Bool,
		ResolutionBreach: row.ResolutionBreach.Bool,
		ResponseAtRisk:   row.ResponseAtRisk.Bool,
		ResolutionAtRisk: row.ResolutionAtRisk.Bool,
	}, nil
}
