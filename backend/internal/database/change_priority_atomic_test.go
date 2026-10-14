package database_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// Every write a priority change makes is in one transaction (#443b). Each store
// call is failed in turn, on real Postgres, after the ones before it have
// written: the priority, the SLA rows and the audit entry must all be as they
// were.
func TestChangePriority_EachFailingStepRollsEverythingBack(t *testing.T) {
	errStep := errors.New("injected failure")
	steps := []string{"Update", "CurrentSLARecordForUpdate", "MatchSLAPolicy", "SupersedeSLARecord", "StartSLARecord", "audit"}
	for _, step := range steps {
		t.Run(step, func(t *testing.T) {
			f := newChangePriorityFx(t)
			require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: f.tk.ID, PolicyID: f.pol[ticket.PriorityMedium].ID}))
			require.NoError(t, f.ss.SetFirstResponse(f.ctx, f.tk.ID, time.Now().UTC().Truncate(time.Millisecond), 600, 1800))
			rowsBefore := f.records(t)
			ticketBefore, err := f.ts.GetByID(f.ctx, f.tk.ID)
			require.NoError(t, err)

			svc := f.service(t, failingStepAtomic{db: f.db.SQL, step: step, err: errStep})
			_, err = svc.ChangePriority(f.ctx, f.tk.ID, ticket.PriorityHigh, f.staff)
			require.ErrorIs(t, err, errStep)

			stored, err := f.ts.GetByID(f.ctx, f.tk.ID)
			require.NoError(t, err)
			require.Equal(t, ticketBefore.Priority, stored.Priority)
			require.True(t, ticketBefore.UpdatedAt.Equal(stored.UpdatedAt))
			require.Equal(t, rowsBefore, f.records(t), "no supersede, no new record")
			require.Empty(t, f.actions(t))
		})
	}
}

// failingStepAtomic is the production transaction with one store call that
// fails after its real write has been made, so the rollback has something to
// undo.
type failingStepAtomic struct {
	db   *sql.DB
	step string
	err  error
}

func (a failingStepAtomic) InTx(ctx context.Context, fn func(ticket.Store, audit.Store) error) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := dbgen.New(tx)
	st := failingStepStore{Store: ticketstore.New(q), step: a.step, err: a.err}
	var au audit.Store = auditstore.New(q)
	if a.step == "audit" {
		au = failingStepAudit{Store: au, err: a.err}
	}
	if err := fn(st, au); err != nil {
		return err
	}
	return tx.Commit()
}

type failingStepStore struct {
	ticket.Store
	step string
	err  error
}

func (s failingStepStore) fail(step string, err error) error {
	if err != nil {
		return err
	}
	if s.step == step {
		return s.err
	}
	return nil
}

func (s failingStepStore) Update(ctx context.Context, t ticket.Ticket) error {
	return s.fail("Update", s.Store.Update(ctx, t))
}

func (s failingStepStore) CurrentSLARecordForUpdate(ctx context.Context, id uuid.UUID) (*ticket.SLARecordState, error) {
	r, err := s.Store.CurrentSLARecordForUpdate(ctx, id)
	return r, s.fail("CurrentSLARecordForUpdate", err)
}

func (s failingStepStore) MatchSLAPolicy(ctx context.Context, p ticket.Priority, c uuid.UUID) (*uuid.UUID, error) {
	r, err := s.Store.MatchSLAPolicy(ctx, p, c)
	return r, s.fail("MatchSLAPolicy", err)
}

func (s failingStepStore) SupersedeSLARecord(ctx context.Context, id uuid.UUID, at time.Time) error {
	return s.fail("SupersedeSLARecord", s.Store.SupersedeSLARecord(ctx, id, at))
}

func (s failingStepStore) StartSLARecord(ctx context.Context, r ticket.SLARecordStart) error {
	return s.fail("StartSLARecord", s.Store.StartSLARecord(ctx, r))
}

type failingStepAudit struct {
	audit.Store
	err error
}

func (a failingStepAudit) Create(ctx context.Context, e audit.Entry) error {
	if err := a.Store.Create(ctx, e); err != nil {
		return err
	}
	return a.err
}
