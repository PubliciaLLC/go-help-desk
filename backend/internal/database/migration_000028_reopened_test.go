package database_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/slastore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// skip000027AlterTable skips migration 000027's ALTER TABLE — the columns it
// adds already exist in the test database's schema.
func skip000027AlterTable(stmt string) bool {
	return strings.HasPrefix(strings.ToUpper(stmt), "ALTER TABLE")
}

// runMigration027And028 runs 000027 (skipping its ALTER TABLE) and then 000028
// against tx, in their real upgrade order, both read verbatim from disk.
func runMigration027And028(t *testing.T, ctx context.Context, tx *sql.Tx) {
	t.Helper()
	execMigrationFile(t, ctx, tx, "migrations/000027_sla_frozen_elapsed.up.sql", skip000027AlterTable)
	execMigrationFile(t, ctx, tx, "migrations/000028_repair_resolved_status_invariant.up.sql", nil)
}

// migration028TestFixture bundles the stores and reference data every test in
// this file needs, so each test function only has to build tickets.
type migration028TestFixture struct {
	tx       *sql.Tx
	ctx      context.Context
	us       *userstore.Store
	cs       *categorystore.Store
	ts       *ticketstore.Store
	sls      *slastore.Store
	cat      category.Category
	policy   sla.Policy
	reporter user.User

	newSt, resolvedSt, closedSt, inProgressSt, pendingSt ticket.Status
}

func newMigration028Fixture(t *testing.T, namePrefix string) *migration028TestFixture {
	t.Helper()
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)
	ctx := context.Background()

	tx, err := db.SQL.Begin()
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	q := dbgen.New(tx)

	us := userstore.New(q)
	cs := categorystore.New(q)
	ts := ticketstore.New(q)
	sls := slastore.New(q)

	reporter := user.User{
		ID: uuid.New(), Email: namePrefix + "-" + uuid.NewString() + "@test.local",
		DisplayName: "Reporter", Role: user.RoleUser,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, reporter))
	cat := category.Category{ID: uuid.New(), Name: namePrefix + " " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))

	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)
	resolvedSt, err := ts.GetStatusByName(ctx, ticket.StatusNameResolved)
	require.NoError(t, err)
	closedSt, err := ts.GetStatusByName(ctx, ticket.StatusNameClosed)
	require.NoError(t, err)
	inProgressSt, err := ts.GetStatusByName(ctx, "In Progress")
	require.NoError(t, err)
	pendingSt, err := ts.GetStatusByName(ctx, ticket.StatusNamePending)
	require.NoError(t, err)

	// Policy 30/30, per the design's test plan default.
	policy := sla.Policy{ID: uuid.New(), Name: namePrefix + " policy", ResponseTargetMin: 30, ResolutionTargetMin: 30}
	require.NoError(t, sls.CreatePolicy(ctx, policy))

	f := &migration028TestFixture{
		tx: tx, ctx: ctx, us: us, cs: cs, ts: ts, sls: sls, cat: cat, policy: policy,
		newSt: newSt, resolvedSt: resolvedSt, closedSt: closedSt, inProgressSt: inProgressSt, pendingSt: pendingSt,
	}
	f.reporter = reporter
	return f
}

func (f *migration028TestFixture) reporterID() *uuid.UUID { return &f.reporter.ID }

// seed creates a ticket with the given status/timestamps and an sla_records
// row with every field NULL, via seedTicketState so updated_at lands exactly
// as given rather than at time.Now().
func (f *migration028TestFixture) seed(t *testing.T, subject string, statusID uuid.UUID, created, updated time.Time, resolvedAt, closedAt, pendingSince *time.Time) ticket.Ticket {
	t.Helper()
	tk := ticket.Ticket{
		ID:             uuid.New(),
		TrackingNumber: ticket.TrackingNumber("M28-" + uuid.NewString()[:8]),
		Subject:        subject,
		Description:    subject,
		CategoryID:     f.cat.ID,
		Priority:       ticket.PriorityMedium,
		StatusID:       f.newSt.ID,
		ReporterUserID: f.reporterID(),
		CreatedAt:      created,
		UpdatedAt:      updated,
	}
	require.NoError(t, f.ts.Create(f.ctx, tk))
	tk.StatusID = statusID
	tk.ResolvedAt = resolvedAt
	tk.ClosedAt = closedAt
	tk.PendingSince = pendingSince
	seedTicketState(t, f.ctx, f.tx, tk)
	require.NoError(t, f.sls.CreateRecord(f.ctx, sla.Record{TicketID: tk.ID, PolicyID: f.policy.ID}))
	return tk
}

func (f *migration028TestFixture) getTicket(t *testing.T, id uuid.UUID) ticket.Ticket {
	t.Helper()
	tk, err := f.ts.GetByID(f.ctx, id)
	require.NoError(t, err)
	return tk
}

func (f *migration028TestFixture) getRecord(t *testing.T, id uuid.UUID) sla.Record {
	t.Helper()
	rec, err := f.sls.GetRecord(f.ctx, id)
	require.NoError(t, err)
	return rec
}

// TestMigration_SLABackfillUsesEarliestRecordedResolution pins #242 (a Closed
// ticket's earlier Resolved stint must be the SLA resolution instant, not the
// later close) and, along the way, section 5 items 1 and 2 from the design: a
// currently-Resolved ticket resolved twice must use its FIRST resolve
// (item 1), and history recovers a resolution fact even for a status this
// migration does not otherwise repair the tickets row for.
func TestMigration_SLABackfillUsesEarliestRecordedResolution(t *testing.T) {
	f := newMigration028Fixture(t, "backfill-earliest")
	now := time.Now().UTC().Truncate(time.Millisecond)
	T := now.Add(-4 * time.Hour)

	// closedAfterUnstampedResolve: Closed, resolved_at/closed_at both NULL.
	// History: New@T, New->Resolved@T+10m, Resolved->Closed@T+2h.
	closedAfterUnstampedResolve := f.seed(t, "closed after unstamped resolve", f.closedSt.ID, T, now, nil, nil, nil)
	seedHistory(t, f.ctx, f.ts, closedAfterUnstampedResolve.ID, nil, f.newSt.ID, T)
	seedHistory(t, f.ctx, f.ts, closedAfterUnstampedResolve.ID, &f.newSt.ID, f.resolvedSt.ID, T.Add(10*time.Minute))
	seedHistory(t, f.ctx, f.ts, closedAfterUnstampedResolve.ID, &f.resolvedSt.ID, f.closedSt.ID, T.Add(2*time.Hour))

	// closedAfterUnstampedResolveClosedAtSet: same shape, but closed_at is
	// already stamped (T+2h) — must not change the SLA result.
	closedAtAlready := T.Add(2 * time.Hour)
	closedAfterUnstampedResolveClosedAtSet := f.seed(t, "closed after unstamped resolve, closed_at set",
		f.closedSt.ID, T, now, nil, &closedAtAlready, nil)
	seedHistory(t, f.ctx, f.ts, closedAfterUnstampedResolveClosedAtSet.ID, nil, f.newSt.ID, T)
	seedHistory(t, f.ctx, f.ts, closedAfterUnstampedResolveClosedAtSet.ID, &f.newSt.ID, f.resolvedSt.ID, T.Add(10*time.Minute))
	seedHistory(t, f.ctx, f.ts, closedAfterUnstampedResolveClosedAtSet.ID, &f.resolvedSt.ID, f.closedSt.ID, T.Add(2*time.Hour))

	// closedAfterLateUnstampedResolve: same shape, but the resolve itself was
	// late (T+1h, past the 30-minute target).
	closedAfterLateUnstampedResolve := f.seed(t, "closed after late unstamped resolve", f.closedSt.ID, T, now, nil, nil, nil)
	seedHistory(t, f.ctx, f.ts, closedAfterLateUnstampedResolve.ID, nil, f.newSt.ID, T)
	seedHistory(t, f.ctx, f.ts, closedAfterLateUnstampedResolve.ID, &f.newSt.ID, f.resolvedSt.ID, T.Add(1*time.Hour))
	seedHistory(t, f.ctx, f.ts, closedAfterLateUnstampedResolve.ID, &f.resolvedSt.ID, f.closedSt.ID, T.Add(2*time.Hour))

	// resolvedTwice: currently Resolved, resolved_at = T+3h (its LATEST
	// resolve). History: New@T, New->Resolved@T+10m, Resolved->InProgress@T+1h,
	// InProgress->Resolved@T+3h.
	resolvedTwiceAt := T.Add(3 * time.Hour)
	resolvedTwice := f.seed(t, "resolved twice", f.resolvedSt.ID, T, now, &resolvedTwiceAt, nil, nil)
	seedHistory(t, f.ctx, f.ts, resolvedTwice.ID, nil, f.newSt.ID, T)
	seedHistory(t, f.ctx, f.ts, resolvedTwice.ID, &f.newSt.ID, f.resolvedSt.ID, T.Add(10*time.Minute))
	seedHistory(t, f.ctx, f.ts, resolvedTwice.ID, &f.resolvedSt.ID, f.inProgressSt.ID, T.Add(1*time.Hour))
	seedHistory(t, f.ctx, f.ts, resolvedTwice.ID, &f.inProgressSt.ID, f.resolvedSt.ID, T.Add(3*time.Hour))

	// closedInconsistentHistory: Closed, closed_at/resolved_at both NULL,
	// updated_at = T+3h. History: New->Closed@T+10m, Closed->InProgress@T+1h
	// (the re-close back to Closed is not recorded).
	closedInconsistentHistory := f.seed(t, "closed inconsistent history", f.closedSt.ID, T, T.Add(3*time.Hour), nil, nil, nil)
	seedHistory(t, f.ctx, f.ts, closedInconsistentHistory.ID, &f.newSt.ID, f.closedSt.ID, T.Add(10*time.Minute))
	seedHistory(t, f.ctx, f.ts, closedInconsistentHistory.ID, &f.closedSt.ID, f.inProgressSt.ID, T.Add(1*time.Hour))

	runMigration027And028(t, f.ctx, f.tx)

	// closedAfterUnstampedResolve
	tk1 := f.getTicket(t, closedAfterUnstampedResolve.ID)
	require.NotNil(t, tk1.ClosedAt, "R4 must stamp closed_at from history")
	require.True(t, tk1.ClosedAt.Equal(T.Add(2*time.Hour)))
	require.Nil(t, tk1.ResolvedAt, "tickets.resolved_at stays NULL: it was never a fact on the tickets row")
	rec1 := f.getRecord(t, closedAfterUnstampedResolve.ID)
	require.NotNil(t, rec1.ResolvedAt, "#242: the earlier Resolved stint must be captured")
	require.True(t, rec1.ResolvedAt.Equal(T.Add(10*time.Minute)), "must be the resolve instant, not the later close")
	require.NotNil(t, rec1.ResolutionElapsedAtMetSeconds)
	require.Equal(t, int64(600), *rec1.ResolutionElapsedAtMetSeconds)
	require.Nil(t, rec1.ResolutionBreachedAt, "on-time resolve: no breach")
	require.Nil(t, rec1.ResponseBreachedAt)
	require.NotNil(t, rec1.FirstResponseAt)
	require.True(t, rec1.FirstResponseAt.Equal(T.Add(10*time.Minute)))

	// closedAfterUnstampedResolveClosedAtSet: identical SLA result, closed_at
	// unchanged.
	tk2 := f.getTicket(t, closedAfterUnstampedResolveClosedAtSet.ID)
	require.NotNil(t, tk2.ClosedAt)
	require.True(t, tk2.ClosedAt.Equal(closedAtAlready))
	rec2 := f.getRecord(t, closedAfterUnstampedResolveClosedAtSet.ID)
	require.NotNil(t, rec2.ResolvedAt)
	require.True(t, rec2.ResolvedAt.Equal(T.Add(10*time.Minute)))
	require.Nil(t, rec2.ResolutionBreachedAt)

	// closedAfterLateUnstampedResolve: the resolve itself stamps both, since
	// it really was late and it is a fact, not an estimate.
	rec3 := f.getRecord(t, closedAfterLateUnstampedResolve.ID)
	require.NotNil(t, rec3.ResolvedAt)
	require.True(t, rec3.ResolvedAt.Equal(T.Add(1*time.Hour)))
	require.NotNil(t, rec3.ResolutionBreachedAt, "#242: a genuinely late fact must still stamp a breach")
	require.True(t, rec3.ResolutionBreachedAt.Equal(T.Add(1*time.Hour)))
	require.NotNil(t, rec3.ResponseBreachedAt)
	require.True(t, rec3.ResponseBreachedAt.Equal(T.Add(1*time.Hour)))

	// resolvedTwice: R2 does not touch tickets.resolved_at (already set); the
	// SLA record must use the FIRST resolve, not the current stint's resolve.
	tk4 := f.getTicket(t, resolvedTwice.ID)
	require.NotNil(t, tk4.ResolvedAt)
	require.True(t, tk4.ResolvedAt.Equal(resolvedTwiceAt), "R2 never overwrites an already-set resolved_at")
	rec4 := f.getRecord(t, resolvedTwice.ID)
	require.NotNil(t, rec4.ResolvedAt)
	require.True(t, rec4.ResolvedAt.Equal(T.Add(10*time.Minute)),
		"section 5 item 1: the SLA record must keep the FIRST resolve, not the later re-resolve")
	require.Nil(t, rec4.ResolutionBreachedAt, "the old SQL (T+3h) would have falsely stamped this")
	require.Nil(t, rec4.ResponseBreachedAt)

	// closedInconsistentHistory: R4 falls back to updated_at (T+3h) because
	// the history is inconsistent, but C1's own close-arm history lookup still
	// finds the real close fact (T+10m) independently, and it is NOT marked
	// estimated.
	tk5 := f.getTicket(t, closedInconsistentHistory.ID)
	require.NotNil(t, tk5.ClosedAt)
	require.True(t, tk5.ClosedAt.Equal(T.Add(3*time.Hour)), "R4's own fallback is unchanged by this fix")
	rec5 := f.getRecord(t, closedInconsistentHistory.ID)
	require.NotNil(t, rec5.ResolvedAt)
	require.True(t, rec5.ResolvedAt.Equal(T.Add(10*time.Minute)), "C1's close-arm history fact, not R4's fallback")
	require.Nil(t, rec5.ResolutionBreachedAt, "a fact-backed on-time instant must not be stamped")
	require.Nil(t, rec5.ResponseBreachedAt)
}

// TestMigration_SLABackfillPreservesReopenedTicketsResolution pins #244: a
// ticket moved off Resolved (R1 clears tickets.resolved_at) must not lose the
// resolution its sla_records row needs — C1 captures it before R1 runs.
func TestMigration_SLABackfillPreservesReopenedTicketsResolution(t *testing.T) {
	f := newMigration028Fixture(t, "backfill-reopened")
	now := time.Now().UTC().Truncate(time.Millisecond)
	T := now.Add(-4 * time.Hour)

	// reopenedOnTime: In Progress, resolved_at = T+20m, no history.
	reopenedOnTimeAt := T.Add(20 * time.Minute)
	reopenedOnTime := f.seed(t, "reopened on time", f.inProgressSt.ID, T, now, &reopenedOnTimeAt, nil, nil)

	// reopenedLate: In Progress, resolved_at = T+1h.
	reopenedLateAt := T.Add(1 * time.Hour)
	reopenedLate := f.seed(t, "reopened late", f.inProgressSt.ID, T, now, &reopenedLateAt, nil, nil)

	// reopenedPending: Pending, resolved_at = T+20m, pending_since = T+2h.
	reopenedPendingAt := T.Add(20 * time.Minute)
	pendingSince := T.Add(2 * time.Hour)
	reopenedPending := f.seed(t, "reopened pending", f.pendingSt.ID, T, now, &reopenedPendingAt, nil, &pendingSince)

	// reopenedCustom: a custom status, resolved_at = T+20m.
	var customStatusID uuid.UUID
	require.NoError(t, f.tx.QueryRowContext(f.ctx,
		`INSERT INTO statuses (name, kind, sort_order, color) VALUES ($1, 'custom', 60, '#000000') RETURNING id`,
		"Mig028 reopened custom "+uuid.NewString()[:8]).Scan(&customStatusID))
	reopenedCustomAt := T.Add(20 * time.Minute)
	reopenedCustom := f.seed(t, "reopened custom", customStatusID, T, now, &reopenedCustomAt, nil, nil)

	// reopenedHistoryOnly: In Progress, resolved_at NULL. History:
	// New->Resolved@T+15m, Resolved->InProgress@T+1h.
	reopenedHistoryOnly := f.seed(t, "reopened history only", f.inProgressSt.ID, T, now, nil, nil, nil)
	seedHistory(t, f.ctx, f.ts, reopenedHistoryOnly.ID, &f.newSt.ID, f.resolvedSt.ID, T.Add(15*time.Minute))
	seedHistory(t, f.ctx, f.ts, reopenedHistoryOnly.ID, &f.resolvedSt.ID, f.inProgressSt.ID, T.Add(1*time.Hour))

	// reopenedStaleLaterThanHistory: In Progress, resolved_at = T+3h (stale,
	// later than the real history fact). History: New->Resolved@T+10m.
	staleAt := T.Add(3 * time.Hour)
	reopenedStaleLaterThanHistory := f.seed(t, "reopened stale later than history", f.inProgressSt.ID, T, now, &staleAt, nil, nil)
	seedHistory(t, f.ctx, f.ts, reopenedStaleLaterThanHistory.ID, &f.newSt.ID, f.resolvedSt.ID, T.Add(10*time.Minute))

	runMigration027And028(t, f.ctx, f.tx)

	// reopenedOnTime
	tkOnTime := f.getTicket(t, reopenedOnTime.ID)
	require.Nil(t, tkOnTime.ResolvedAt, "R1 clears resolved_at for a non-terminal status")
	recOnTime := f.getRecord(t, reopenedOnTime.ID)
	require.NotNil(t, recOnTime.ResolvedAt)
	require.True(t, recOnTime.ResolvedAt.Equal(reopenedOnTimeAt))
	require.NotNil(t, recOnTime.ResolutionElapsedAtMetSeconds)
	require.Equal(t, int64(1200), *recOnTime.ResolutionElapsedAtMetSeconds)
	require.Nil(t, recOnTime.ResolutionBreachedAt)
	require.Nil(t, recOnTime.ResponseBreachedAt)
	require.NotNil(t, recOnTime.FirstResponseAt)
	require.True(t, recOnTime.FirstResponseAt.Equal(reopenedOnTimeAt))
	candidates, err := f.sls.ListBreachCandidates(f.ctx, now)
	require.NoError(t, err)
	for _, id := range candidates {
		require.NotEqual(t, reopenedOnTime.ID, id, "#244: both targets are set and met, so this must not be a live candidate")
	}

	// reopenedLate
	recLate := f.getRecord(t, reopenedLate.ID)
	require.NotNil(t, recLate.ResolutionBreachedAt)
	require.True(t, recLate.ResolutionBreachedAt.Equal(reopenedLateAt))
	require.NotNil(t, recLate.ResponseBreachedAt)
	require.True(t, recLate.ResponseBreachedAt.Equal(reopenedLateAt))

	// reopenedPending: response_elapsed = 1200 pins that the pending clip is 0
	// when the resolution instant is BEFORE pending_since.
	recPending := f.getRecord(t, reopenedPending.ID)
	require.NotNil(t, recPending.ResponseElapsedAtMetSeconds)
	require.Equal(t, int64(1200), *recPending.ResponseElapsedAtMetSeconds)

	// reopenedCustom: same as reopenedOnTime.
	recCustom := f.getRecord(t, reopenedCustom.ID)
	require.NotNil(t, recCustom.ResolvedAt)
	require.True(t, recCustom.ResolvedAt.Equal(reopenedCustomAt))
	require.Nil(t, recCustom.ResolutionBreachedAt)
	require.Nil(t, recCustom.ResponseBreachedAt)

	// reopenedHistoryOnly: the deliberate broadening (section 5 item 2) — no
	// fact on the tickets row at all, recovered purely from history.
	recHistoryOnly := f.getRecord(t, reopenedHistoryOnly.ID)
	require.NotNil(t, recHistoryOnly.ResolvedAt)
	require.True(t, recHistoryOnly.ResolvedAt.Equal(T.Add(15*time.Minute)))
	require.Nil(t, recHistoryOnly.ResolutionBreachedAt)
	require.Nil(t, recHistoryOnly.ResponseBreachedAt)

	// reopenedStaleLaterThanHistory: the earlier history fact wins over the
	// later stale resolved_at.
	recStale := f.getRecord(t, reopenedStaleLaterThanHistory.ID)
	require.NotNil(t, recStale.ResolvedAt)
	require.True(t, recStale.ResolvedAt.Equal(T.Add(10*time.Minute)))
}

// TestMigration_EstimatedResolutionFreezesButNeverStamps pins #243(a): a
// resolution instant recovered only from the updated_at fallback (no fact
// anywhere) must still get sla_records.resolved_at, a frozen elapsed reading,
// and a first_response_at — but never a breach stamp.
func TestMigration_EstimatedResolutionFreezesButNeverStamps(t *testing.T) {
	f := newMigration028Fixture(t, "backfill-estimated")
	now := time.Now().UTC().Truncate(time.Millisecond)
	T := now.Add(-4 * time.Hour)
	updatedAt := T.Add(3 * time.Hour)

	// closedNoHistory: Closed, both timestamps NULL, no history.
	closedNoHistory := f.seed(t, "closed no history", f.closedSt.ID, T, updatedAt, nil, nil, nil)

	// resolvedNoHistory: Resolved, resolved_at NULL, no history.
	resolvedNoHistory := f.seed(t, "resolved no history", f.resolvedSt.ID, T, updatedAt, nil, nil, nil)

	// estimatedWithRealLateResponse: as closedNoHistory, but the sla_records
	// row already carries a REAL first_response_at (T+2h) from before the
	// migration ran.
	realFirstResponseAt := T.Add(2 * time.Hour)
	estimatedWithRealLateResponse := f.seed(t, "closed no history, real late response", f.closedSt.ID, T, updatedAt, nil, nil, nil)
	// f.seed already created this ticket's sla_records row with every field
	// NULL; overwrite it with one carrying a real first_response_at.
	_, err := f.tx.ExecContext(f.ctx,
		`UPDATE sla_records SET first_response_at = $2 WHERE ticket_id = $1`,
		estimatedWithRealLateResponse.ID, realFirstResponseAt)
	require.NoError(t, err)

	runMigration027And028(t, f.ctx, f.tx)

	// closedNoHistory
	tkClosed := f.getTicket(t, closedNoHistory.ID)
	require.NotNil(t, tkClosed.ClosedAt)
	require.True(t, tkClosed.ClosedAt.Equal(updatedAt), "R4 fallback: no history, no fact")
	recClosed := f.getRecord(t, closedNoHistory.ID)
	require.NotNil(t, recClosed.ResolvedAt)
	require.True(t, recClosed.ResolvedAt.Equal(updatedAt))
	require.NotNil(t, recClosed.ResolutionElapsedAtMetSeconds)
	require.Equal(t, int64(10800), *recClosed.ResolutionElapsedAtMetSeconds)
	require.Nil(t, recClosed.ResolutionBreachedAt, "#243: an estimated instant must never be stamped, however late it reads")
	require.NotNil(t, recClosed.FirstResponseAt)
	require.True(t, recClosed.FirstResponseAt.Equal(updatedAt))
	require.NotNil(t, recClosed.ResponseElapsedAtMetSeconds)
	require.Equal(t, int64(10800), *recClosed.ResponseElapsedAtMetSeconds)
	require.Nil(t, recClosed.ResponseBreachedAt, "#243: same, for the #226(a) response cascade")

	// resolvedNoHistory
	tkResolved := f.getTicket(t, resolvedNoHistory.ID)
	require.NotNil(t, tkResolved.ResolvedAt)
	require.True(t, tkResolved.ResolvedAt.Equal(updatedAt))
	recResolved := f.getRecord(t, resolvedNoHistory.ID)
	require.NotNil(t, recResolved.ResolvedAt)
	require.True(t, recResolved.ResolvedAt.Equal(updatedAt))
	require.Nil(t, recResolved.ResolutionBreachedAt)
	require.Nil(t, recResolved.ResponseBreachedAt)
	candidates, err := f.sls.ListBreachCandidates(f.ctx, now)
	require.NoError(t, err)
	for _, id := range candidates {
		require.NotEqual(t, resolvedNoHistory.ID, id,
			"once backfilled, this ticket must no longer be a live breach candidate")
	}

	// estimatedWithRealLateResponse: the S6 equality gate lets the REAL late
	// response stamp a breach, even though the resolution itself is estimated.
	recMixed := f.getRecord(t, estimatedWithRealLateResponse.ID)
	require.NotNil(t, recMixed.FirstResponseAt)
	require.True(t, recMixed.FirstResponseAt.Equal(realFirstResponseAt), "S4 must not overwrite an existing first_response_at")
	require.NotNil(t, recMixed.ResponseBreachedAt, "a REAL late response must be stamped even on an estimated-resolution row")
	require.True(t, recMixed.ResponseBreachedAt.Equal(realFirstResponseAt))
	require.Nil(t, recMixed.ResolutionBreachedAt, "the resolution instant itself is still only estimated")

	// Cleanup check: the scratch temp table must not survive the migration.
	var regclass sql.NullString
	require.NoError(t, f.tx.QueryRowContext(f.ctx, `SELECT to_regclass('pg_temp.m28_sla_resolution')::text`).Scan(&regclass))
	require.False(t, regclass.Valid, "m28_sla_resolution must be dropped by the end of the migration")
}
