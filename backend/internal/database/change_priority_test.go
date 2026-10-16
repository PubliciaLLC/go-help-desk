package database_test

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/slastore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/txrunner"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// ChangePriority against real Postgres (#443b). The rows are committed, because
// the properties under test (rollback, the row lock, the unique current record)
// are about transactions, and a rolled-back harness transaction would hide
// them. Every policy is narrowed to a category created here, so nothing else
// sharing the test database can match it.

type changePriorityFx struct {
	ctx   context.Context
	db    *testutil.DB
	q     *dbgen.Queries
	ts    *ticketstore.Store
	ss    *slastore.Store
	au    *auditstore.Store
	tk    ticket.Ticket
	staff ticket.Actor
	pol   map[ticket.Priority]sla.Policy
}

func newChangePriorityFx(t *testing.T) *changePriorityFx {
	t.Helper()
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB) // LIFO: runs after the row cleanup below
	ctx := context.Background()
	q := db.Queries
	f := &changePriorityFx{
		ctx: ctx, db: db, q: q, ts: ticketstore.New(q), ss: slastore.New(q), au: auditstore.New(q),
		pol: map[ticket.Priority]sla.Policy{},
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	reporter := user.User{
		ID: uuid.New(), Email: "cp-" + uuid.NewString() + "@test.local", DisplayName: "Reporter",
		Role: user.RoleUser, CreatedAt: now, UpdatedAt: now,
	}
	staff := user.User{
		ID: uuid.New(), Email: "cp-staff-" + uuid.NewString() + "@test.local", DisplayName: "Staff",
		Role: user.RoleStaff, CreatedAt: now, UpdatedAt: now,
	}
	us := userstore.New(q)
	require.NoError(t, us.Create(ctx, reporter))
	require.NoError(t, us.Create(ctx, staff))
	cat := category.Category{ID: uuid.New(), Name: "ChangePriority " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, categorystore.New(q).CreateCategory(ctx, cat))
	f.staff = ticket.Actor{UserID: &staff.ID, Role: user.RoleStaff}

	newSt, err := f.ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)
	f.tk = ticket.Ticket{
		ID: uuid.New(), TrackingNumber: ticket.TrackingNumber("CP-" + uuid.NewString()[:8]),
		Subject: "change priority", Description: "change priority", CategoryID: cat.ID,
		Priority: ticket.PriorityMedium, StatusID: newSt.ID, ReporterUserID: &reporter.ID,
		CreatedAt: now.Add(-3 * time.Hour), UpdatedAt: now,
	}
	require.NoError(t, f.ts.Create(ctx, f.tk))

	t.Cleanup(func() {
		_, _ = db.SQL.Exec(`DELETE FROM tickets WHERE id = $1`, f.tk.ID) // cascades to sla_records
		_, _ = db.SQL.Exec(`DELETE FROM audit_log WHERE entity_id = $1`, f.tk.ID)
		_, _ = db.SQL.Exec(`DELETE FROM sla_policies WHERE category_id = $1`, cat.ID)
		_, _ = db.SQL.Exec(`DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{reporter.ID, staff.ID})
		_, _ = db.SQL.Exec(`DELETE FROM categories WHERE id = $1`, cat.ID)
	})

	for _, p := range []ticket.Priority{ticket.PriorityMedium, ticket.PriorityHigh, ticket.PriorityCritical} {
		p := p
		pol := sla.Policy{
			ID: uuid.New(), Name: "cp " + string(p), Priority: &p, CategoryID: &cat.ID,
			ResponseTargetMin: 30, ResolutionTargetMin: 120,
		}
		require.NoError(t, f.ss.CreatePolicy(ctx, pol))
		f.pol[p] = pol
	}
	return f
}

// service wires a ticket.Service over a real transaction runner, or over atomic
// when given, with the SLA switched on.
func (f *changePriorityFx) service(t *testing.T, atomic ticket.Atomic) *ticket.Service {
	t.Helper()
	if atomic == nil {
		atomic = txrunner.New(f.db.SQL, nil, nil)
	}
	svc := ticket.NewService(f.ts, f.ts, notification.Noop{}, f.au, atomic, nil)
	require.NoError(t, svc.LoadSystemStatuses(f.ctx))
	svc.SetSLAEnabled(func(context.Context) bool { return true })
	return svc
}

func (f *changePriorityFx) records(t *testing.T) []dbgen.SlaRecord {
	t.Helper()
	rows, err := f.q.ListSLARecordsForTicket(f.ctx, f.tk.ID)
	require.NoError(t, err)
	return rows
}

func (f *changePriorityFx) actions(t *testing.T) map[string]int {
	t.Helper()
	entries, err := f.au.ListByEntity(f.ctx, "ticket", f.tk.ID, 100, 0)
	require.NoError(t, err)
	out := map[string]int{}
	for _, e := range entries {
		out[e.Action]++
	}
	return out
}

// A staff change against real rows: the old row is equal column by column except
// superseded_at, and the new one is current under the new policy with the same
// clock and the same facts. Nothing restarts.
func TestChangePriority_RematchCarriesTheClockAndFactsAgainstRealRows(t *testing.T) {
	f := newChangePriorityFx(t)
	svc := f.service(t, nil)

	require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: f.tk.ID, PolicyID: f.pol[ticket.PriorityMedium].ID}))
	require.NoError(t, f.ss.SetFirstResponse(f.ctx, f.tk.ID, time.Now().UTC().Truncate(time.Millisecond), 600, 1800))
	require.NoError(t, f.ss.StampBreaches(f.ctx, f.tk.ID, nil, ptrTime(time.Now().UTC().Truncate(time.Millisecond))))
	oldRow := f.records(t)[0]

	got, err := svc.ChangePriority(f.ctx, f.tk.ID, ticket.PriorityHigh, f.staff)
	require.NoError(t, err)
	require.Equal(t, ticket.PriorityHigh, got.Priority)

	stored, err := f.ts.GetByID(f.ctx, f.tk.ID)
	require.NoError(t, err)
	require.Equal(t, ticket.PriorityHigh, stored.Priority)

	rows := f.records(t)
	require.Len(t, rows, 2)
	cur, old := rows[0], rows[1] // newest first; equal clocks tie-break on superseded_at
	require.Equal(t, oldRow.ID, old.ID)
	require.True(t, old.SupersededAt.Valid)
	old.SupersededAt = sql.NullTime{}
	require.Equal(t, oldRow, old, "the old row is equal column by column except superseded_at")

	require.False(t, cur.SupersededAt.Valid)
	require.NotEqual(t, oldRow.ID, cur.ID)
	require.Equal(t, f.pol[ticket.PriorityHigh].ID, cur.PolicyID)
	require.Equal(t, oldRow.StartedAt, cur.StartedAt, "the clock start is kept")
	require.Equal(t, oldRow.PausedSecondsAtStart, cur.PausedSecondsAtStart)
	require.Equal(t, oldRow.FirstResponseAt, cur.FirstResponseAt, "a target already met stays met")
	require.Equal(t, oldRow.ResponseElapsedAtMetSeconds, cur.ResponseElapsedAtMetSeconds)
	require.Equal(t, oldRow.ResolutionBreachedAt, cur.ResolutionBreachedAt)

	// And it reads as the ticket's record.
	rec, err := f.ss.GetRecord(f.ctx, f.tk.ID)
	require.NoError(t, err)
	require.Equal(t, cur.ID, rec.ID)
	require.NotNil(t, rec.FirstResponseAt)

	require.Equal(t, map[string]int{"priority_changed": 1}, f.actions(t))
}

// A ticket with no record yet whose new priority matches a policy gets one
// whose clock started at the ticket's creation, not at the change (Erik, Q4).
func TestChangePriority_NoRecordYetStartsFromTicketCreationAgainstRealRows(t *testing.T) {
	f := newChangePriorityFx(t)
	svc := f.service(t, nil)

	_, err := svc.ChangePriority(f.ctx, f.tk.ID, ticket.PriorityHigh, f.staff)
	require.NoError(t, err)

	rows := f.records(t)
	require.Len(t, rows, 1)
	require.Equal(t, f.pol[ticket.PriorityHigh].ID, rows[0].PolicyID)
	require.False(t, rows[0].StartedAt.Valid, "no start: measured from ticket creation")

	// The ticket is three hours old and the policy's response target 30 minutes.
	rec, err := f.ss.GetRecord(f.ctx, f.tk.ID)
	require.NoError(t, err)
	stored, err := f.ts.GetByID(f.ctx, f.tk.ID)
	require.NoError(t, err)
	require.True(t, sla.IsResponseBreached(rec, f.pol[ticket.PriorityHigh], stored, time.Now()), "breached at once, as decided")
}

// A Resolved ticket is refused with the closed-ticket conflict, priority
// included, against the real row lock.
func TestChangePriority_ResolvedTicketIsRefusedAgainstRealRows(t *testing.T) {
	f := newChangePriorityFx(t)
	svc := f.service(t, nil)
	_, err := svc.Resolve(f.ctx, f.tk.ID, "done", f.staff)
	require.NoError(t, err)

	_, err = svc.ChangePriority(f.ctx, f.tk.ID, ticket.PriorityHigh, f.staff)
	require.ErrorIs(t, err, ticket.ErrClosed)

	stored, err := f.ts.GetByID(f.ctx, f.tk.ID)
	require.NoError(t, err)
	require.Equal(t, ticket.PriorityMedium, stored.Priority)
	require.Empty(t, f.records(t))
}

// The audit write is part of the change. Failing it, after the priority, the
// supersede and the new record have all been written, must leave the database
// as it was.
func TestChangePriority_AuditFailureRollsBackAgainstRealRows(t *testing.T) {
	f := newChangePriorityFx(t)
	svc := f.service(t, failingAuditAtomic{db: f.db.SQL, failAction: "priority_changed"})

	require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: f.tk.ID, PolicyID: f.pol[ticket.PriorityMedium].ID}))
	require.NoError(t, f.ss.SetFirstResponse(f.ctx, f.tk.ID, time.Now().UTC().Truncate(time.Millisecond), 600, 1800))
	rowsBefore := f.records(t)

	_, err := svc.ChangePriority(f.ctx, f.tk.ID, ticket.PriorityHigh, f.staff)
	require.ErrorIs(t, err, errAuditDown)

	stored, err := f.ts.GetByID(f.ctx, f.tk.ID)
	require.NoError(t, err)
	require.Equal(t, ticket.PriorityMedium, stored.Priority, "the priority is rolled back")
	require.Equal(t, rowsBefore, f.records(t), "no supersede, no new record")
	require.Empty(t, f.actions(t), "the first audit entry is rolled back with the rest")
}

// Two changes at once serialise on the ticket's row lock: both land, each
// re-matches the record the other left, and exactly one record is current.
func TestChangePriority_ConcurrentChangesLeaveOneCurrentRecord(t *testing.T) {
	f := newChangePriorityFx(t)
	svc := f.service(t, nil)
	require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: f.tk.ID, PolicyID: f.pol[ticket.PriorityMedium].ID}))

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, p := range []ticket.Priority{ticket.PriorityHigh, ticket.PriorityCritical} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = svc.ChangePriority(f.ctx, f.tk.ID, p, f.staff)
		}()
	}
	wg.Wait()
	require.NoError(t, errs[0])
	require.NoError(t, errs[1])

	rows := f.records(t)
	require.Len(t, rows, 3, "the original and one record per change")
	var current []dbgen.SlaRecord
	for _, r := range rows {
		if !r.SupersededAt.Valid {
			current = append(current, r)
		}
	}
	require.Len(t, current, 1, "exactly one record is current")

	stored, err := f.ts.GetByID(f.ctx, f.tk.ID)
	require.NoError(t, err)
	require.Equal(t, f.pol[stored.Priority].ID, current[0].PolicyID, "the current record is under the final priority's policy")
	require.Equal(t, map[string]int{"priority_changed": 2}, f.actions(t))
}

func ptrTime(t time.Time) *time.Time { return &t }

// failingAuditAtomic is the production transaction shape with an audit store
// that refuses one action, so a test can fail a write in the middle of a change
// and check what the database does about it.
var errAuditDown = auditDownError("audit store is down")

type auditDownError string

func (e auditDownError) Error() string { return string(e) }

type failingAuditAtomic struct {
	db         *sql.DB
	failAction string
}

func (a failingAuditAtomic) InTx(ctx context.Context, fn func(ticket.Store, audit.Store, *notification.Changes) error) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := dbgen.New(tx)
	if err := fn(ticketstore.New(q), refusingAudit{Store: auditstore.New(q), action: a.failAction}, &notification.Changes{}); err != nil {
		return err
	}
	return tx.Commit()
}

type refusingAudit struct {
	audit.Store
	action string
}

func (r refusingAudit) Create(ctx context.Context, e audit.Entry) error {
	if e.Action == r.action {
		return errAuditDown
	}
	return r.Store.Create(ctx, e)
}
