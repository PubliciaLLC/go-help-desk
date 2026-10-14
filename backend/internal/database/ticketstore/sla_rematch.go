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
		Settled: r.FirstResponseAt.Valid || r.ResolvedAt.Valid ||
			r.ResponseBreachedAt.Valid || r.ResolutionBreachedAt.Valid,
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
	}); err != nil {
		return fmt.Errorf("starting SLA record for ticket %s: %w", r.TicketID, err)
	}
	return nil
}
