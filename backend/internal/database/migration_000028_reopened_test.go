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

	// reopenedPendingBeforeResolution (#250): Pending, resolved_at = T+40m (a
	// legacy dropped-history shape: a stamped resolve whose history row was
	// dropped, then moved back to Pending without a history row either),
	// pending_since = T+10m — BEFORE the captured resolution instant, unlike
	// reopenedPending above (whose pending_since is AFTER, so its clip is a
	// no-op). Without S1's pending-time clip this would freeze 2400s and S3
	// would falsely stamp a resolution breach against the 30-minute target,
	// while S5's already-clipped response side would read 600s and stay
	// on-time for the exact same instant — inconsistent with live
	// sla.Elapsed, which always clips. With the clip both sides must agree.
	resolvedBeforePendingAt := T.Add(40 * time.Minute)
	pendingSinceBeforeResolve := T.Add(10 * time.Minute)
	reopenedPendingBeforeResolution := f.seed(t, "reopened pending before resolution",
		f.pendingSt.ID, T, now, &resolvedBeforePendingAt, nil, &pendingSinceBeforeResolve)

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

	// reopenedPendingBeforeResolution (#250): the opposite shape — pending_since
	// BEFORE the resolution instant, so the clip actually subtracts time. Both
	// the resolution and response elapsed readings must equal what live
	// sla.Elapsed computes for this exact ticket/instant, and neither target
	// may be stamped as breached (correctly on time once clipped).
	wantElapsed := sla.Elapsed(ticket.Ticket{
		CreatedAt:        T,
		PendingSince:     &pendingSinceBeforeResolve,
		SLAPausedSeconds: 0,
	}, resolvedBeforePendingAt)
	recPendingBefore := f.getRecord(t, reopenedPendingBeforeResolution.ID)
	require.NotNil(t, recPendingBefore.ResolutionElapsedAtMetSeconds)
	require.Equal(t, int64(wantElapsed/time.Second), *recPendingBefore.ResolutionElapsedAtMetSeconds,
		"#250: S1 must clip pending time the same way live sla.Elapsed does")
	require.NotNil(t, recPendingBefore.ResponseElapsedAtMetSeconds)
	require.Equal(t, *recPendingBefore.ResolutionElapsedAtMetSeconds, *recPendingBefore.ResponseElapsedAtMetSeconds,
		"resolution and response elapsed must agree: they are the same instant")
	require.Nil(t, recPendingBefore.ResolutionBreachedAt, "#250: correctly clipped, this is on time")
	require.Nil(t, recPendingBefore.ResponseBreachedAt, "#250: correctly clipped, this is on time")

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

// TestMigration_SLABackfillCorrectsFirstResponsePostdatingResolution pins
// #253: a real first_response_at already recorded on the sla_records row can
// postdate the ticket's own (earlier) FACT resolution — reachable from
// ordinary v1.2.0-era legacy data, where RecordResolved and
// RecordFirstResponse were independent first-write-wins calls with no #219
// folding. S4 (mirroring the live COALESCE) leaves an already-set
// first_response_at untouched, so without S4a's fix the later reply would
// stay recorded as the response even though the earlier resolution should
// count as one first, per #219/#220 and the same "earliest fact wins"
// principle #247 already established on the resolution-capture side.
func TestMigration_SLABackfillCorrectsFirstResponsePostdatingResolution(t *testing.T) {
	f := newMigration028Fixture(t, "backfill-response-postdate")
	now := time.Now().UTC().Truncate(time.Millisecond)
	T := now.Add(-4 * time.Hour)

	// caseA (the issue's own reproduction): resolved on time at T+10m with no
	// reply yet, reopened at T+60m, given a real reply at T+70m — AFTER the
	// resolution — while reopened, then re-resolved at T+80m. The sla_records
	// row already carries first_response_at = T+70m before this migration
	// runs, simulating the independent first-write-wins RecordFirstResponse
	// call v1.2.0 would have made.
	firstResolveAt := T.Add(10 * time.Minute)
	reopenAt := T.Add(60 * time.Minute)
	replyAt := T.Add(70 * time.Minute)
	reResolveAt := T.Add(80 * time.Minute)
	lateReplyAfterOnTimeResolve := f.seed(t, "late reply after on-time resolve, re-resolved",
		f.resolvedSt.ID, T, now, &reResolveAt, nil, nil)
	seedHistory(t, f.ctx, f.ts, lateReplyAfterOnTimeResolve.ID, nil, f.newSt.ID, T)
	seedHistory(t, f.ctx, f.ts, lateReplyAfterOnTimeResolve.ID, &f.newSt.ID, f.resolvedSt.ID, firstResolveAt)
	seedHistory(t, f.ctx, f.ts, lateReplyAfterOnTimeResolve.ID, &f.resolvedSt.ID, f.inProgressSt.ID, reopenAt)
	seedHistory(t, f.ctx, f.ts, lateReplyAfterOnTimeResolve.ID, &f.inProgressSt.ID, f.resolvedSt.ID, reResolveAt)
	_, err := f.tx.ExecContext(f.ctx,
		`UPDATE sla_records SET first_response_at = $2 WHERE ticket_id = $1`,
		lateReplyAfterOnTimeResolve.ID, replyAt)
	require.NoError(t, err)

	// caseB (control): a real reply that genuinely precedes the resolution —
	// must be completely unaffected by #253's fix. resolved_at is already
	// stamped on the tickets row (T+10m); the reply is manually set to T+5m,
	// before it.
	resolveAtCaseB := T.Add(10 * time.Minute)
	earlyReplyAt := T.Add(5 * time.Minute)
	replyBeforeResolve := f.seed(t, "reply genuinely precedes resolve (control)",
		f.resolvedSt.ID, T, now, &resolveAtCaseB, nil, nil)
	_, err = f.tx.ExecContext(f.ctx,
		`UPDATE sla_records SET first_response_at = $2 WHERE ticket_id = $1`,
		replyBeforeResolve.ID, earlyReplyAt)
	require.NoError(t, err)

	runMigration027And028(t, f.ctx, f.tx)

	// caseA: the SLA record must use the FIRST (on-time) resolve, not the
	// re-resolve, and #253's fix must rewind first_response_at back to that
	// same earlier instant, freezing response_elapsed_at_met_seconds to the
	// SAME value already frozen for the resolution — not recomputed
	// independently — with no false response breach left over from the
	// pre-correction reply.
	recA := f.getRecord(t, lateReplyAfterOnTimeResolve.ID)
	require.NotNil(t, recA.ResolvedAt)
	require.True(t, recA.ResolvedAt.Equal(firstResolveAt),
		"the SLA record must keep the FIRST (on-time) resolve, not the re-resolve")
	require.NotNil(t, recA.ResolutionElapsedAtMetSeconds)
	require.Equal(t, int64(600), *recA.ResolutionElapsedAtMetSeconds)
	require.Nil(t, recA.ResolutionBreachedAt, "on-time resolution: no breach")
	require.NotNil(t, recA.FirstResponseAt)
	require.True(t, recA.FirstResponseAt.Equal(firstResolveAt),
		"#253: a real reply that postdates a FACT resolution must be corrected back to the earlier resolution instant")
	require.NotNil(t, recA.ResponseElapsedAtMetSeconds)
	require.Equal(t, *recA.ResolutionElapsedAtMetSeconds, *recA.ResponseElapsedAtMetSeconds,
		"#253: response elapsed must reuse the SAME frozen value as the resolution, not be recomputed independently")
	require.Nil(t, recA.ResponseBreachedAt,
		"#253: no false response breach from the pre-correction reply, which was 60 minutes past the 30-minute target")

	// caseB: completely unaffected — the reply already precedes the
	// resolution, so S4a's `first_response_at > resolved_at` predicate never
	// matches.
	recB := f.getRecord(t, replyBeforeResolve.ID)
	require.NotNil(t, recB.ResolvedAt)
	require.True(t, recB.ResolvedAt.Equal(resolveAtCaseB))
	require.NotNil(t, recB.ResolutionElapsedAtMetSeconds)
	require.Equal(t, int64(600), *recB.ResolutionElapsedAtMetSeconds)
	require.Nil(t, recB.ResolutionBreachedAt)
	require.NotNil(t, recB.FirstResponseAt)
	require.True(t, recB.FirstResponseAt.Equal(earlyReplyAt),
		"#253 control: a reply that genuinely precedes the resolution must be completely unaffected")
	require.NotNil(t, recB.ResponseElapsedAtMetSeconds)
	require.Equal(t, int64(300), *recB.ResponseElapsedAtMetSeconds)
	require.Nil(t, recB.ResponseBreachedAt)
}

// TestMigration_SLABackfillCorrectsFirstResponsePostdatingEstimatedResolution
// pins #255: the ESTIMATED twin of #253/S4a. An estimated resolution instant
// (recovered only from the updated_at fallback, no fact anywhere) can ALSO
// have a real, later first_response_at already recorded on the sla_records
// row — a genuine staff reply, or (pre-#221) an internal note, posted after
// the ticket went terminal. Because updated_at is an upper bound on the true
// transition, the true transition happened no later than the estimate, which
// is therefore no later than the reply either, so the estimate itself is the
// correct stand-in for the first response — not the later reply. Without
// S4b, S4 leaves first_response_at untouched (already non-NULL), S4a's
// fact-only gate skips an estimated row entirely, and 000027's own earlier,
// unconditional response-freeze pass has already frozen
// response_elapsed_at_met_seconds from the untouched real reply, so S6 would
// stamp a false response breach from that frozen (but wrong) number.
func TestMigration_SLABackfillCorrectsFirstResponsePostdatingEstimatedResolution(t *testing.T) {
	f := newMigration028Fixture(t, "backfill-estimated-response-postdate")
	now := time.Now().UTC().Truncate(time.Millisecond)
	T := now.Add(-6 * time.Hour)

	setFirstResponse := func(id uuid.UUID, at time.Time) {
		_, err := f.tx.ExecContext(f.ctx,
			`UPDATE sla_records SET first_response_at = $2 WHERE ticket_id = $1`, id, at)
		require.NoError(t, err)
	}

	// caseA: Closed, no history, resolved_at/closed_at both NULL, updated_at =
	// T+10m (an ON-TIME estimate — well inside the 30-minute target), with a
	// real reply recorded well after it (T+2h). Without S4b this reply's
	// frozen elapsed (7200s) would falsely breach a ticket whose true
	// transition provably happened no later than T+10m.
	onTimeEstimateAt := T.Add(10 * time.Minute)
	lateRealReplyA := T.Add(2 * time.Hour)
	closedOnTimeEstimateLateReply := f.seed(t, "closed on-time estimate, late real reply",
		f.closedSt.ID, T, onTimeEstimateAt, nil, nil, nil)
	setFirstResponse(closedOnTimeEstimateLateReply.ID, lateRealReplyA)

	// caseB: identical shape, but Resolved instead of Closed (R2's fallback
	// instead of R4's).
	lateRealReplyB := T.Add(3 * time.Hour)
	resolvedOnTimeEstimateLateReply := f.seed(t, "resolved on-time estimate, late real reply",
		f.resolvedSt.ID, T, onTimeEstimateAt, nil, nil, nil)
	setFirstResponse(resolvedOnTimeEstimateLateReply.ID, lateRealReplyB)

	// caseC: a LATE-estimate variant (Closed) — the estimate itself is past
	// the 30-minute target (updated_at = T+3h), with a real reply later still
	// (T+5h). Even though the estimate reads as late, it must still never be
	// frozen or stamped (#246's invariant for every estimated instant), and
	// the corrected first_response_at must be the estimate, not the reply.
	lateEstimateAt := T.Add(3 * time.Hour)
	lateRealReplyC := T.Add(5 * time.Hour)
	closedLateEstimateLateReply := f.seed(t, "closed late estimate, later real reply",
		f.closedSt.ID, T, lateEstimateAt, nil, nil, nil)
	setFirstResponse(closedLateEstimateLateReply.ID, lateRealReplyC)

	runMigration027And028(t, f.ctx, f.tx)

	// caseA
	tkA := f.getTicket(t, closedOnTimeEstimateLateReply.ID)
	require.NotNil(t, tkA.ClosedAt, "R4 fallback: no history, no fact")
	require.True(t, tkA.ClosedAt.Equal(onTimeEstimateAt))
	recA := f.getRecord(t, closedOnTimeEstimateLateReply.ID)
	require.NotNil(t, recA.ResolvedAt)
	require.True(t, recA.ResolvedAt.Equal(onTimeEstimateAt), "estimated resolution instant")
	require.Nil(t, recA.ResolutionElapsedAtMetSeconds, "estimated: never frozen")
	require.Nil(t, recA.ResolutionBreachedAt, "estimated: never stamped")
	require.NotNil(t, recA.FirstResponseAt)
	require.True(t, recA.FirstResponseAt.Equal(onTimeEstimateAt),
		"#255: S4b must correct first_response_at back to the estimate, not leave the later real reply")
	require.Nil(t, recA.ResponseElapsedAtMetSeconds,
		"#255: S4b must explicitly clear the number 000027's earlier pass froze from the untouched real reply")
	require.Nil(t, recA.ResponseBreachedAt,
		"#255: no false response breach — the reply was 110 minutes past the 30-minute target before the fix")

	// caseB: same shape, Resolved.
	tkB := f.getTicket(t, resolvedOnTimeEstimateLateReply.ID)
	require.NotNil(t, tkB.ResolvedAt, "R2 fallback: no history, no fact")
	require.True(t, tkB.ResolvedAt.Equal(onTimeEstimateAt))
	recB := f.getRecord(t, resolvedOnTimeEstimateLateReply.ID)
	require.NotNil(t, recB.ResolvedAt)
	require.True(t, recB.ResolvedAt.Equal(onTimeEstimateAt))
	require.Nil(t, recB.ResolutionElapsedAtMetSeconds)
	require.Nil(t, recB.ResolutionBreachedAt)
	require.NotNil(t, recB.FirstResponseAt)
	require.True(t, recB.FirstResponseAt.Equal(onTimeEstimateAt),
		"#255: same correction for a Resolved ticket")
	require.Nil(t, recB.ResponseElapsedAtMetSeconds)
	require.Nil(t, recB.ResponseBreachedAt)

	// caseC: late-estimate variant — still recorded, unfrozen, unstamped.
	tkC := f.getTicket(t, closedLateEstimateLateReply.ID)
	require.NotNil(t, tkC.ClosedAt)
	require.True(t, tkC.ClosedAt.Equal(lateEstimateAt))
	recC := f.getRecord(t, closedLateEstimateLateReply.ID)
	require.NotNil(t, recC.ResolvedAt)
	require.True(t, recC.ResolvedAt.Equal(lateEstimateAt))
	require.Nil(t, recC.ResolutionElapsedAtMetSeconds, "estimated: never frozen, however late it reads")
	require.Nil(t, recC.ResolutionBreachedAt, "estimated: never stamped, however late it reads")
	require.NotNil(t, recC.FirstResponseAt)
	require.True(t, recC.FirstResponseAt.Equal(lateEstimateAt),
		"#255: corrected to the (late) estimate, not the even-later real reply")
	require.Nil(t, recC.ResponseElapsedAtMetSeconds,
		"#255: an estimated instant is never frozen on the response side either, on time or late")
	require.Nil(t, recC.ResponseBreachedAt)
}

// TestMigration_EstimatedResolutionIsRecordedButNeitherFrozenNorStamped pins
// #243(a) and #246: a resolution instant recovered only from the updated_at
// fallback (no fact anywhere) must still get sla_records.resolved_at and a
// first_response_at — but, after #246, neither a frozen elapsed reading nor
// a breach stamp. (Renamed from …FreezesButNeverStamps: before #246 this
// migration froze an estimated instant's elapsed reading, which is exactly
// what made a second run of the file unable to tell "already handled"
// apart from "needs handling", and go on to stamp a false breach on rerun —
// see the migration's LIMITS section.)
func TestMigration_EstimatedResolutionIsRecordedButNeitherFrozenNorStamped(t *testing.T) {
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
	require.Nil(t, recClosed.ResolutionElapsedAtMetSeconds,
		"#246: an estimated instant must never be frozen — the NULL is the durable marker a second run reads")
	require.Nil(t, recClosed.ResolutionBreachedAt, "#243: an estimated instant must never be stamped, however late it reads")
	require.NotNil(t, recClosed.FirstResponseAt)
	require.True(t, recClosed.FirstResponseAt.Equal(updatedAt))
	require.Nil(t, recClosed.ResponseElapsedAtMetSeconds,
		"#246: same, for the #226(a) response cascade — S5's gate must exclude S4's copy of an estimated instant")
	require.Nil(t, recClosed.ResponseBreachedAt, "#243: same, for the #226(a) response cascade")

	// The live indicator must still read this as a late (red) resolution, and
	// IsResolutionBreached must still agree, via the live-recompute fallback
	// targetStatus/IsResolutionBreached use for a NULL frozen elapsed value —
	// "no stamp" must never mean "reads as on time" (DESIGN.md allows red
	// without a stamp; it does not allow green instead of red).
	statusClosed := sla.StatusFor(recClosed, f.policy, tkClosed, now.Add(30*24*time.Hour))
	require.Equal(t, sla.Red, statusClosed.Resolution.Color)
	require.Equal(t, 180, statusClosed.Resolution.ElapsedMin, "3h elapsed against a 30-minute target")
	require.NotNil(t, statusClosed.Resolution.MetAt)
	require.True(t, statusClosed.Resolution.MetAt.Equal(updatedAt))
	require.Equal(t, sla.Red, statusClosed.Response.Color)
	require.True(t, sla.IsResolutionBreached(recClosed, f.policy, tkClosed, now.Add(30*24*time.Hour)),
		"IsResolutionBreached must fall back to the same live recompute as the indicator, not read NULL as met")

	// resolvedNoHistory
	tkResolved := f.getTicket(t, resolvedNoHistory.ID)
	require.NotNil(t, tkResolved.ResolvedAt)
	require.True(t, tkResolved.ResolvedAt.Equal(updatedAt))
	recResolved := f.getRecord(t, resolvedNoHistory.ID)
	require.NotNil(t, recResolved.ResolvedAt)
	require.True(t, recResolved.ResolvedAt.Equal(updatedAt))
	require.Nil(t, recResolved.ResolutionElapsedAtMetSeconds, "#246: estimated, never frozen")
	require.Nil(t, recResolved.ResolutionBreachedAt)
	require.Nil(t, recResolved.ResponseElapsedAtMetSeconds, "#246: estimated, never frozen")
	require.Nil(t, recResolved.ResponseBreachedAt)
	candidates, err := f.sls.ListBreachCandidates(f.ctx, now)
	require.NoError(t, err)
	for _, id := range candidates {
		require.NotEqual(t, resolvedNoHistory.ID, id,
			"once backfilled (resolved_at and first_response_at both set), this ticket must no longer be a live breach candidate, even with no frozen elapsed number")
		require.NotEqual(t, closedNoHistory.ID, id, "same, for the Closed fixture")
	}

	// estimatedWithRealLateResponse: the S5 equality gate lets the REAL late
	// response freeze and stamp normally, even though the resolution itself
	// is estimated.
	recMixed := f.getRecord(t, estimatedWithRealLateResponse.ID)
	require.NotNil(t, recMixed.FirstResponseAt)
	require.True(t, recMixed.FirstResponseAt.Equal(realFirstResponseAt), "S4 must not overwrite an existing first_response_at")
	require.Nil(t, recMixed.ResolutionElapsedAtMetSeconds, "the resolution instant itself is still only estimated")
	require.Nil(t, recMixed.ResolutionBreachedAt, "the resolution instant itself is still only estimated")
	require.NotNil(t, recMixed.ResponseElapsedAtMetSeconds,
		"S5's gate must NOT exclude a REAL first_response_at merely because the ticket's resolution is estimated")
	require.Equal(t, int64(7200), *recMixed.ResponseElapsedAtMetSeconds)
	require.NotNil(t, recMixed.ResponseBreachedAt, "a REAL late response must be stamped even on an estimated-resolution row")
	require.True(t, recMixed.ResponseBreachedAt.Equal(realFirstResponseAt))

	// Cleanup check: the scratch temp table must not survive the migration.
	var regclass sql.NullString
	require.NoError(t, f.tx.QueryRowContext(f.ctx, `SELECT to_regclass('pg_temp.m28_sla_resolution')::text`).Scan(&regclass))
	require.False(t, regclass.Valid, "m28_sla_resolution must be dropped by the end of the migration")
}

// TestMigration_SLABackfillClosedTicketUsesEarliestResolveOrClose pins #247:
// C1's close arm must be COMPARED against the resolve arm via a flat LEAST,
// not consulted only when the resolve arm is NULL. Before this fix, a ticket
// that resolved late, was reopened, and was resolved again on time before
// being re-closed would record its LATER (on-time) resolve instead of its
// earlier (late) close — turning what should read as a breach into a false
// on-time record, and worse, flipping it on a later migration run if the
// on-time resolve fact were added to history only after the ticket was
// already closed once.
func TestMigration_SLABackfillClosedTicketUsesEarliestResolveOrClose(t *testing.T) {
	f := newMigration028Fixture(t, "backfill-close-le")
	now := time.Now().UTC().Truncate(time.Millisecond)
	T := now.Add(-4 * time.Hour)

	// closeReopenResolveClose: Closed, resolved_at T+120m, closed_at T+150m
	// (both facts already stamped on the tickets row — R1/R4 must not need to
	// touch them for C1 to find the right instant). History: New@T,
	// New->Closed@T+10m, Closed->InProgress@T+60m, InProgress->Resolved@T+120m,
	// Resolved->Closed@T+150m. The issue's reproduction shape: pre-fix, the
	// old COALESCE only consulted the close arm when the resolve arm (T+120m)
	// was NULL, so it recorded T+120m — 7200s against a 30-minute target, a
	// breach — even though the ticket was first (and validly) closed at
	// T+10m, on time, before ever being reopened and resolved again.
	closedAt1 := T.Add(150 * time.Minute)
	resolvedAt1 := T.Add(120 * time.Minute)
	closeReopenResolveClose := f.seed(t, "close reopen resolve close", f.closedSt.ID, T, now, &resolvedAt1, &closedAt1, nil)
	seedHistory(t, f.ctx, f.ts, closeReopenResolveClose.ID, nil, f.newSt.ID, T)
	seedHistory(t, f.ctx, f.ts, closeReopenResolveClose.ID, &f.newSt.ID, f.closedSt.ID, T.Add(10*time.Minute))
	seedHistory(t, f.ctx, f.ts, closeReopenResolveClose.ID, &f.closedSt.ID, f.inProgressSt.ID, T.Add(60*time.Minute))
	seedHistory(t, f.ctx, f.ts, closeReopenResolveClose.ID, &f.inProgressSt.ID, f.resolvedSt.ID, T.Add(120*time.Minute))
	seedHistory(t, f.ctx, f.ts, closeReopenResolveClose.ID, &f.resolvedSt.ID, f.closedSt.ID, T.Add(150*time.Minute))

	// closeReopenResolveCloseHistoryOnly: identical history, but NEITHER
	// ticket column is stamped — proves C1 reads the fact from history alone,
	// and R4 still recovers closed_at (T+150m) independently.
	closeReopenResolveCloseHistoryOnly := f.seed(t, "close reopen resolve close, history only", f.closedSt.ID, T, now, nil, nil, nil)
	seedHistory(t, f.ctx, f.ts, closeReopenResolveCloseHistoryOnly.ID, nil, f.newSt.ID, T)
	seedHistory(t, f.ctx, f.ts, closeReopenResolveCloseHistoryOnly.ID, &f.newSt.ID, f.closedSt.ID, T.Add(10*time.Minute))
	seedHistory(t, f.ctx, f.ts, closeReopenResolveCloseHistoryOnly.ID, &f.closedSt.ID, f.inProgressSt.ID, T.Add(60*time.Minute))
	seedHistory(t, f.ctx, f.ts, closeReopenResolveCloseHistoryOnly.ID, &f.inProgressSt.ID, f.resolvedSt.ID, T.Add(120*time.Minute))
	seedHistory(t, f.ctx, f.ts, closeReopenResolveCloseHistoryOnly.ID, &f.resolvedSt.ID, f.closedSt.ID, T.Add(150*time.Minute))

	// closeReopenResolveStillResolved (#238 sibling control): currently
	// Resolved (never re-closed), resolved_at T+120m. The close arm must be
	// gated off entirely (status is Resolved, not Closed), so this must be
	// UNCHANGED by #247: T+120m, late, both stamps.
	resolvedAt2 := T.Add(120 * time.Minute)
	closeReopenResolveStillResolved := f.seed(t, "close reopen resolve, still resolved", f.resolvedSt.ID, T, now, &resolvedAt2, nil, nil)
	seedHistory(t, f.ctx, f.ts, closeReopenResolveStillResolved.ID, nil, f.newSt.ID, T)
	seedHistory(t, f.ctx, f.ts, closeReopenResolveStillResolved.ID, &f.newSt.ID, f.closedSt.ID, T.Add(10*time.Minute))
	seedHistory(t, f.ctx, f.ts, closeReopenResolveStillResolved.ID, &f.closedSt.ID, f.inProgressSt.ID, T.Add(60*time.Minute))
	seedHistory(t, f.ctx, f.ts, closeReopenResolveStillResolved.ID, &f.inProgressSt.ID, f.resolvedSt.ID, T.Add(120*time.Minute))

	// closeReopenResolveReopened (#239/#244 control): In Progress again (the
	// resolve was itself reopened, never re-closed). resolved_at NULL on the
	// tickets row (R1 clears it). The close arm must be gated off (status is
	// In Progress, not Closed), so C1 must still use the resolve arm: T+120m,
	// late, both stamps — exactly as #244 already requires, now proven
	// alongside a close earlier in the same history.
	closeReopenResolveReopened := f.seed(t, "close reopen resolve reopened", f.inProgressSt.ID, T, now, nil, nil, nil)
	seedHistory(t, f.ctx, f.ts, closeReopenResolveReopened.ID, nil, f.newSt.ID, T)
	seedHistory(t, f.ctx, f.ts, closeReopenResolveReopened.ID, &f.newSt.ID, f.closedSt.ID, T.Add(10*time.Minute))
	seedHistory(t, f.ctx, f.ts, closeReopenResolveReopened.ID, &f.closedSt.ID, f.inProgressSt.ID, T.Add(60*time.Minute))
	seedHistory(t, f.ctx, f.ts, closeReopenResolveReopened.ID, &f.inProgressSt.ID, f.resolvedSt.ID, T.Add(120*time.Minute))
	seedHistory(t, f.ctx, f.ts, closeReopenResolveReopened.ID, &f.resolvedSt.ID, f.inProgressSt.ID, T.Add(130*time.Minute))

	// closeReopenCloseOnly (the issue's comparison case): Closed, never
	// resolved at all — closed, reopened, and closed again. The resolve arm
	// is NULL throughout (LEAST ignores it), so C1 must fall through to the
	// close arm: T+10m, on time, no stamps.
	closeReopenCloseOnly := f.seed(t, "close reopen close only", f.closedSt.ID, T, now, nil, nil, nil)
	seedHistory(t, f.ctx, f.ts, closeReopenCloseOnly.ID, &f.newSt.ID, f.closedSt.ID, T.Add(10*time.Minute))
	seedHistory(t, f.ctx, f.ts, closeReopenCloseOnly.ID, &f.closedSt.ID, f.inProgressSt.ID, T.Add(60*time.Minute))
	seedHistory(t, f.ctx, f.ts, closeReopenCloseOnly.ID, &f.inProgressSt.ID, f.closedSt.ID, T.Add(180*time.Minute))

	runMigration027And028(t, f.ctx, f.tx)

	rec1 := f.getRecord(t, closeReopenResolveClose.ID)
	require.NotNil(t, rec1.ResolvedAt)
	require.True(t, rec1.ResolvedAt.Equal(T.Add(10*time.Minute)),
		"#247: LEAST must pick the earlier CLOSE fact over the later resolve fact")
	require.NotNil(t, rec1.ResolutionElapsedAtMetSeconds)
	require.Equal(t, int64(600), *rec1.ResolutionElapsedAtMetSeconds)
	require.Nil(t, rec1.ResolutionBreachedAt, "on time: no breach")
	require.Nil(t, rec1.ResponseBreachedAt)
	tk1 := f.getTicket(t, closeReopenResolveClose.ID)
	require.NotNil(t, tk1.ResolvedAt, "R2 does not clear an already-set resolved_at on a Closed ticket")
	require.True(t, tk1.ResolvedAt.Equal(resolvedAt1), "tickets.resolved_at is untouched by #247 — only the SLA record's instant changes")
	require.NotNil(t, tk1.ClosedAt)
	require.True(t, tk1.ClosedAt.Equal(closedAt1))

	rec1b := f.getRecord(t, closeReopenResolveCloseHistoryOnly.ID)
	require.NotNil(t, rec1b.ResolvedAt)
	require.True(t, rec1b.ResolvedAt.Equal(T.Add(10*time.Minute)), "same result, recovered purely from history")
	require.Nil(t, rec1b.ResolutionBreachedAt)
	require.Nil(t, rec1b.ResponseBreachedAt)
	tk1b := f.getTicket(t, closeReopenResolveCloseHistoryOnly.ID)
	require.NotNil(t, tk1b.ClosedAt, "R4 must recover closed_at from history when the ticket row never had one")
	require.True(t, tk1b.ClosedAt.Equal(T.Add(150*time.Minute)))

	rec2 := f.getRecord(t, closeReopenResolveStillResolved.ID)
	require.NotNil(t, rec2.ResolvedAt)
	require.True(t, rec2.ResolvedAt.Equal(resolvedAt2), "#238 sibling: unaffected by #247 — the close arm is gated off for a Resolved ticket")
	require.NotNil(t, rec2.ResolutionBreachedAt, "late: must still stamp")
	require.True(t, rec2.ResolutionBreachedAt.Equal(resolvedAt2))
	require.NotNil(t, rec2.ResponseBreachedAt)

	rec3 := f.getRecord(t, closeReopenResolveReopened.ID)
	require.NotNil(t, rec3.ResolvedAt)
	require.True(t, rec3.ResolvedAt.Equal(T.Add(120*time.Minute)), "#239/#244: the close arm is gated off for a non-Closed ticket, even one closed earlier in its history")
	require.NotNil(t, rec3.ResolutionBreachedAt, "late: must still stamp")
	require.NotNil(t, rec3.ResponseBreachedAt)

	rec4 := f.getRecord(t, closeReopenCloseOnly.ID)
	require.NotNil(t, rec4.ResolvedAt)
	require.True(t, rec4.ResolvedAt.Equal(T.Add(10*time.Minute)), "never resolved: the close arm is the only source, LEAST falls through to it")
	require.Nil(t, rec4.ResolutionBreachedAt)
	require.Nil(t, rec4.ResponseBreachedAt)
}

// requireSameTimePtr compares two *time.Time for the same instant, nil-safe,
// via time.Time.Equal rather than require.Equal — two values read back from
// the same database column can differ in monotonic reading or exact
// wall-clock representation while still naming the identical instant, and a
// reflect-based comparison is the wrong tool for that.
func requireSameTimePtr(t *testing.T, a, b *time.Time, msgAndArgs ...interface{}) {
	t.Helper()
	if a == nil || b == nil {
		require.Equal(t, a == nil, b == nil, msgAndArgs...)
		return
	}
	require.True(t, a.Equal(*b), msgAndArgs...)
}

// requireSameInt64Ptr compares two *int64 for the same value, nil-safe.
func requireSameInt64Ptr(t *testing.T, a, b *int64, msgAndArgs ...interface{}) {
	t.Helper()
	if a == nil || b == nil {
		require.Equal(t, a == nil, b == nil, msgAndArgs...)
		return
	}
	require.Equal(t, *a, *b, msgAndArgs...)
}

// setSLAFields directly overwrites sla_records columns for a ticket, standing
// in for whatever v1.2.0's independent, first-write-wins RecordResolved /
// RecordFirstResponse calls (or an earlier upgrade's 000027) already
// committed before this migration runs.
func setSLAFields(t *testing.T, ctx context.Context, tx *sql.Tx, ticketID uuid.UUID, resolvedAt, firstResponseAt, resolutionBreachedAt *time.Time) {
	t.Helper()
	_, err := tx.ExecContext(ctx,
		`UPDATE sla_records
		    SET resolved_at = $2, first_response_at = $3, resolution_breached_at = $4
		  WHERE ticket_id = $1`,
		ticketID, resolvedAt, firstResponseAt, resolutionBreachedAt)
	require.NoError(t, err)
}

// setSLAResponseBreachedAt directly stamps sla_records.response_breached_at
// for a ticket — setSLAFields has no parameter for this column, and its
// signature is left alone rather than changed for its many other callers
// (#259).
func setSLAResponseBreachedAt(t *testing.T, ctx context.Context, tx *sql.Tx, ticketID uuid.UUID, responseBreachedAt *time.Time) {
	t.Helper()
	_, err := tx.ExecContext(ctx,
		`UPDATE sla_records SET response_breached_at = $2 WHERE ticket_id = $1`,
		ticketID, responseBreachedAt)
	require.NoError(t, err)
}

// TestMigration_SLABackfillRevisesRecordedResolutionToEarlierFact pins #258:
// an sla_records row that ALREADY carries a resolved_at (the ordinary
// population since #238 — v1.2.0's live RecordResolved, or an earlier run of
// this migration) was never compared against an earlier fact by any
// statement in this file before C1b. C1 only looks at a record whose
// resolved_at is still NULL, so two tickets with IDENTICAL history could
// read completely differently depending only on whether something had
// already been recorded — the issue's own reproduction is the CLOSE-fact
// shape (a ticket closed once, reopened, resolved late, and re-closed, whose
// v1.2.0 RecordResolved recorded only the later re-resolve), and the general
// form fixed here (C1b) also covers the identical bug on the RESOLVE-facts
// arm (a ticket resolved once, reopened, and resolved again, whose
// first-write-wins RecordResolved recorded the later re-resolve even though
// tickets.resolved_at itself keeps the first).
//
// Also pins #259: C1b (and S4a/S4b) must revise a row exactly the same way
// whether or not it already carries a breach stamp — ctlPreStampedRecorded
// and ctlPreStampedUnrecorded below must come out identical, and both of
// their pre-existing stamps must survive untouched. rowBLaterReplyResponseStamped
// documents the same rule holds on the response side.
func TestMigration_SLABackfillRevisesRecordedResolutionToEarlierFact(t *testing.T) {
	f := newMigration028Fixture(t, "backfill-revise-earlier-fact")
	now := time.Now().UTC().Truncate(time.Millisecond)
	T := now.Add(-4 * time.Hour)

	// rowA_unrecorded (#258's own comparison baseline): Closed, CRRC history
	// (close, reopen, resolve, close). Ticket: resolved_at T+120m, closed_at
	// T+150m. SLA pre-state: every field NULL — nothing was ever recorded
	// for this ticket before the migration runs, so C1 alone (not C1b) must
	// produce the corrected result.
	resolvedAt1 := T.Add(120 * time.Minute)
	closedAt1 := T.Add(150 * time.Minute)
	rowA := f.seed(t, "row A: unrecorded, close reopen resolve close", f.closedSt.ID, T, now, &resolvedAt1, &closedAt1, nil)
	seedHistory(t, f.ctx, f.ts, rowA.ID, nil, f.newSt.ID, T)
	seedHistory(t, f.ctx, f.ts, rowA.ID, &f.newSt.ID, f.closedSt.ID, T.Add(10*time.Minute))
	seedHistory(t, f.ctx, f.ts, rowA.ID, &f.closedSt.ID, f.inProgressSt.ID, T.Add(60*time.Minute))
	seedHistory(t, f.ctx, f.ts, rowA.ID, &f.inProgressSt.ID, f.resolvedSt.ID, T.Add(120*time.Minute))
	seedHistory(t, f.ctx, f.ts, rowA.ID, &f.resolvedSt.ID, f.closedSt.ID, T.Add(150*time.Minute))

	// rowB_recorded (#258 exactly): identical shape to rowA, but
	// sla_records.resolved_at is pre-set to T+120m — simulating v1.2.0's live
	// RecordResolved, which recorded only the resolve it saw, never compared
	// against the earlier close in ticket_status_history. Without the fix:
	// 7200s frozen and both stamps at T+120m, permanently.
	rowB := f.seed(t, "row B: recorded, close reopen resolve close", f.closedSt.ID, T, now, &resolvedAt1, &closedAt1, nil)
	seedHistory(t, f.ctx, f.ts, rowB.ID, nil, f.newSt.ID, T)
	seedHistory(t, f.ctx, f.ts, rowB.ID, &f.newSt.ID, f.closedSt.ID, T.Add(10*time.Minute))
	seedHistory(t, f.ctx, f.ts, rowB.ID, &f.closedSt.ID, f.inProgressSt.ID, T.Add(60*time.Minute))
	seedHistory(t, f.ctx, f.ts, rowB.ID, &f.inProgressSt.ID, f.resolvedSt.ID, T.Add(120*time.Minute))
	seedHistory(t, f.ctx, f.ts, rowB.ID, &f.resolvedSt.ID, f.closedSt.ID, T.Add(150*time.Minute))
	setSLAFields(t, f.ctx, f.tx, rowB.ID, &resolvedAt1, nil, nil)

	// rowB_laterReply: as rowB, plus a REAL reply recorded at T+70m — after
	// the corrected T+10m instant, but before the stale T+120m one. 000027's
	// unconditional response-freeze pass (which runs before 028 and has no
	// fact/estimate concept) freezes this at 4200s against the OLD resolved
	// instant; S4a must rewind it to T+10m and reuse C1b's own 600s.
	replyAt := T.Add(70 * time.Minute)
	rowBLaterReply := f.seed(t, "row B, later reply", f.closedSt.ID, T, now, &resolvedAt1, &closedAt1, nil)
	seedHistory(t, f.ctx, f.ts, rowBLaterReply.ID, nil, f.newSt.ID, T)
	seedHistory(t, f.ctx, f.ts, rowBLaterReply.ID, &f.newSt.ID, f.closedSt.ID, T.Add(10*time.Minute))
	seedHistory(t, f.ctx, f.ts, rowBLaterReply.ID, &f.closedSt.ID, f.inProgressSt.ID, T.Add(60*time.Minute))
	seedHistory(t, f.ctx, f.ts, rowBLaterReply.ID, &f.inProgressSt.ID, f.resolvedSt.ID, T.Add(120*time.Minute))
	seedHistory(t, f.ctx, f.ts, rowBLaterReply.ID, &f.resolvedSt.ID, f.closedSt.ID, T.Add(150*time.Minute))
	setSLAFields(t, f.ctx, f.tx, rowBLaterReply.ID, &resolvedAt1, &replyAt, nil)

	// rowB_earlierReply: as rowB, plus a REAL reply at T+5m — genuinely
	// before the corrected T+10m instant. Must be completely unaffected: the
	// reply already precedes the resolution, on either side of the fix.
	earlyReplyAt := T.Add(5 * time.Minute)
	rowBEarlierReply := f.seed(t, "row B, earlier reply", f.closedSt.ID, T, now, &resolvedAt1, &closedAt1, nil)
	seedHistory(t, f.ctx, f.ts, rowBEarlierReply.ID, nil, f.newSt.ID, T)
	seedHistory(t, f.ctx, f.ts, rowBEarlierReply.ID, &f.newSt.ID, f.closedSt.ID, T.Add(10*time.Minute))
	seedHistory(t, f.ctx, f.ts, rowBEarlierReply.ID, &f.closedSt.ID, f.inProgressSt.ID, T.Add(60*time.Minute))
	seedHistory(t, f.ctx, f.ts, rowBEarlierReply.ID, &f.inProgressSt.ID, f.resolvedSt.ID, T.Add(120*time.Minute))
	seedHistory(t, f.ctx, f.ts, rowBEarlierReply.ID, &f.resolvedSt.ID, f.closedSt.ID, T.Add(150*time.Minute))
	setSLAFields(t, f.ctx, f.tx, rowBEarlierReply.ID, &resolvedAt1, &earlyReplyAt, nil)

	// rowH_resolveArm: the RESOLVE-facts arm of the identical bug, not #258's
	// own close-fact reproduction. Currently Resolved. Ticket resolved_at is
	// T+10m — v1.2.0's applyStatusTimestamps keeps the FIRST resolve across a
	// Resolved->Resolved transition — but sla_records.resolved_at is pre-set
	// to T+2h, as if a first-write-wins RecordResolved had recorded the LATER
	// re-resolve instead. Without the fix: 7200s frozen, both stamps.
	rowHResolveAt := T.Add(10 * time.Minute)
	rowHReResolveAt := T.Add(2 * time.Hour)
	rowH := f.seed(t, "row H: resolve arm, resolved twice", f.resolvedSt.ID, T, now, &rowHResolveAt, nil, nil)
	seedHistory(t, f.ctx, f.ts, rowH.ID, nil, f.newSt.ID, T)
	seedHistory(t, f.ctx, f.ts, rowH.ID, &f.newSt.ID, f.resolvedSt.ID, rowHResolveAt)
	seedHistory(t, f.ctx, f.ts, rowH.ID, &f.resolvedSt.ID, f.resolvedSt.ID, rowHReResolveAt)
	setSLAFields(t, f.ctx, f.tx, rowH.ID, &rowHReResolveAt, nil, nil)

	// ctlAlreadyEarliestOnTime: a stored resolved_at that is ALREADY the
	// earliest fact must never be revised in the wrong direction. Closed.
	// History: New->Resolved@T+10m, Resolved->Closed@T+60m. Ticket:
	// resolved_at T+10m, closed_at T+60m. SLA: resolved_at T+10m (already
	// correct).
	ctlOnTimeResolveAt := T.Add(10 * time.Minute)
	ctlOnTimeCloseAt := T.Add(60 * time.Minute)
	ctlAlreadyEarliestOnTime := f.seed(t, "control: already earliest, on time", f.closedSt.ID, T, now, &ctlOnTimeResolveAt, &ctlOnTimeCloseAt, nil)
	seedHistory(t, f.ctx, f.ts, ctlAlreadyEarliestOnTime.ID, &f.newSt.ID, f.resolvedSt.ID, ctlOnTimeResolveAt)
	seedHistory(t, f.ctx, f.ts, ctlAlreadyEarliestOnTime.ID, &f.resolvedSt.ID, f.closedSt.ID, ctlOnTimeCloseAt)
	setSLAFields(t, f.ctx, f.tx, ctlAlreadyEarliestOnTime.ID, &ctlOnTimeResolveAt, nil, nil)

	// ctlAlreadyEarliestLate: a genuinely late recorded resolve that is
	// already the earliest fact must still stamp — C1b must not suppress a
	// real breach merely because nothing needs revising. Closed. History:
	// New->Resolved@T+60m, Resolved->Closed@T+90m. Ticket: resolved_at T+60m,
	// closed_at T+90m. SLA: resolved_at T+60m.
	ctlLateResolveAt := T.Add(60 * time.Minute)
	ctlLateCloseAt := T.Add(90 * time.Minute)
	ctlAlreadyEarliestLate := f.seed(t, "control: already earliest, late", f.closedSt.ID, T, now, &ctlLateResolveAt, &ctlLateCloseAt, nil)
	seedHistory(t, f.ctx, f.ts, ctlAlreadyEarliestLate.ID, &f.newSt.ID, f.resolvedSt.ID, ctlLateResolveAt)
	seedHistory(t, f.ctx, f.ts, ctlAlreadyEarliestLate.ID, &f.resolvedSt.ID, f.closedSt.ID, ctlLateCloseAt)
	setSLAFields(t, f.ctx, f.tx, ctlAlreadyEarliestLate.ID, &ctlLateResolveAt, nil, nil)

	// ctlResolvedGatedOff (#238 sibling): currently Resolved (never
	// re-closed), CRR history. The close arm must stay gated off for a
	// ticket that is not currently Closed — unaffected by C1b, exactly as it
	// is unaffected by C1.
	ctlGatedResolveAt := T.Add(120 * time.Minute)
	ctlResolvedGatedOff := f.seed(t, "control: resolved, close arm gated off", f.resolvedSt.ID, T, now, &ctlGatedResolveAt, nil, nil)
	seedHistory(t, f.ctx, f.ts, ctlResolvedGatedOff.ID, nil, f.newSt.ID, T)
	seedHistory(t, f.ctx, f.ts, ctlResolvedGatedOff.ID, &f.newSt.ID, f.closedSt.ID, T.Add(10*time.Minute))
	seedHistory(t, f.ctx, f.ts, ctlResolvedGatedOff.ID, &f.closedSt.ID, f.inProgressSt.ID, T.Add(60*time.Minute))
	seedHistory(t, f.ctx, f.ts, ctlResolvedGatedOff.ID, &f.inProgressSt.ID, f.resolvedSt.ID, T.Add(120*time.Minute))
	setSLAFields(t, f.ctx, f.tx, ctlResolvedGatedOff.ID, &ctlGatedResolveAt, nil, nil)

	// ctlReopenedGatedOff: In Progress (reopened again after the re-resolve),
	// CRR history plus a further Resolved->InProgress. Close arm gated off
	// (not currently Closed), same as ctlResolvedGatedOff.
	ctlReopenedGatedOff := f.seed(t, "control: reopened, close arm gated off", f.inProgressSt.ID, T, now, &ctlGatedResolveAt, nil, nil)
	seedHistory(t, f.ctx, f.ts, ctlReopenedGatedOff.ID, nil, f.newSt.ID, T)
	seedHistory(t, f.ctx, f.ts, ctlReopenedGatedOff.ID, &f.newSt.ID, f.closedSt.ID, T.Add(10*time.Minute))
	seedHistory(t, f.ctx, f.ts, ctlReopenedGatedOff.ID, &f.closedSt.ID, f.inProgressSt.ID, T.Add(60*time.Minute))
	seedHistory(t, f.ctx, f.ts, ctlReopenedGatedOff.ID, &f.inProgressSt.ID, f.resolvedSt.ID, T.Add(120*time.Minute))
	seedHistory(t, f.ctx, f.ts, ctlReopenedGatedOff.ID, &f.resolvedSt.ID, f.inProgressSt.ID, T.Add(130*time.Minute))
	setSLAFields(t, f.ctx, f.tx, ctlReopenedGatedOff.ID, &ctlGatedResolveAt, nil, nil)

	// ctlPreStampedRecorded (#259): rowB's exact shape, plus an existing
	// breach stamp on BOTH targets at T+60m — simulating the untagged
	// v1.3.0-beta branch's own breach sweep having already stamped this row
	// (its first_response_at stays NULL, since the beta sweep stamps
	// response_breached_at without ever recording a response instant) before
	// this file's C1b ever runs (section 5(e)/(3)(e) of the design). A stamp
	// is a fact and must never be contradicted or cleared, but per #259 it
	// must not block C1b's (or S4a/S4b's) revision either: this row's
	// resolved_at/first_response_at must come out identical to
	// ctlPreStampedUnrecorded below, and both stamps must survive untouched.
	ctlPreStampedAt := T.Add(60 * time.Minute)
	ctlPreStampedRecorded := f.seed(t, "control: pre-stamped (recorded), close reopen resolve close", f.closedSt.ID, T, now, &resolvedAt1, &closedAt1, nil)
	seedHistory(t, f.ctx, f.ts, ctlPreStampedRecorded.ID, nil, f.newSt.ID, T)
	seedHistory(t, f.ctx, f.ts, ctlPreStampedRecorded.ID, &f.newSt.ID, f.closedSt.ID, T.Add(10*time.Minute))
	seedHistory(t, f.ctx, f.ts, ctlPreStampedRecorded.ID, &f.closedSt.ID, f.inProgressSt.ID, T.Add(60*time.Minute))
	seedHistory(t, f.ctx, f.ts, ctlPreStampedRecorded.ID, &f.inProgressSt.ID, f.resolvedSt.ID, T.Add(120*time.Minute))
	seedHistory(t, f.ctx, f.ts, ctlPreStampedRecorded.ID, &f.resolvedSt.ID, f.closedSt.ID, T.Add(150*time.Minute))
	setSLAFields(t, f.ctx, f.tx, ctlPreStampedRecorded.ID, &resolvedAt1, nil, &ctlPreStampedAt)
	setSLAResponseBreachedAt(t, f.ctx, f.tx, ctlPreStampedRecorded.ID, &ctlPreStampedAt)

	// ctlPreStampedUnrecorded (#259, the D1/D2 regression): IDENTICAL ticket
	// state, history and both pre-existing stamps as ctlPreStampedRecorded
	// above, but sla_records.resolved_at (and first_response_at) are left
	// NULL — nothing was ever recorded for this row before the migration
	// runs, so C1 (not C1b) produces its result. Before the fix, comparing
	// the two against each other is exactly #259's reproduction: this row
	// resolves via C1, which was never gated on a stamp, and lands on the
	// T+10m close fact; ctlPreStampedRecorded's C1b was gated on
	// `resolution_breached_at IS NULL` and, seeing the stamp, left it
	// stuck at T+120m — two tickets with identical history diverging purely
	// on whether something had already been recorded. After the fix both
	// land on T+10m/600, with both stamps preserved exactly where they were.
	ctlPreStampedUnrecorded := f.seed(t, "control: pre-stamped (unrecorded), close reopen resolve close", f.closedSt.ID, T, now, &resolvedAt1, &closedAt1, nil)
	seedHistory(t, f.ctx, f.ts, ctlPreStampedUnrecorded.ID, nil, f.newSt.ID, T)
	seedHistory(t, f.ctx, f.ts, ctlPreStampedUnrecorded.ID, &f.newSt.ID, f.closedSt.ID, T.Add(10*time.Minute))
	seedHistory(t, f.ctx, f.ts, ctlPreStampedUnrecorded.ID, &f.closedSt.ID, f.inProgressSt.ID, T.Add(60*time.Minute))
	seedHistory(t, f.ctx, f.ts, ctlPreStampedUnrecorded.ID, &f.inProgressSt.ID, f.resolvedSt.ID, T.Add(120*time.Minute))
	seedHistory(t, f.ctx, f.ts, ctlPreStampedUnrecorded.ID, &f.resolvedSt.ID, f.closedSt.ID, T.Add(150*time.Minute))
	setSLAFields(t, f.ctx, f.tx, ctlPreStampedUnrecorded.ID, nil, nil, &ctlPreStampedAt)
	setSLAResponseBreachedAt(t, f.ctx, f.tx, ctlPreStampedUnrecorded.ID, &ctlPreStampedAt)

	// rowBLaterReplyResponseStamped (#259 part 2, pins the chosen policy on
	// the response side): rowB's shape, with a real reply recorded at the
	// stale T+120m instant (not rewound yet) and only response_breached_at
	// pre-stamped (at T+65m) — the resolution side is unstamped. This
	// documents that S4a follows the same "never gated on a stamp" rule as
	// C1b, so a future review does not raise #259 in reverse by adding a
	// stamp gate to S4a alone. This shape already passes today: the gate
	// #259 removes was only ever on resolution_breached_at, never on
	// response_breached_at.
	rowBLaterReplyResponseStampedAt := T.Add(120 * time.Minute)
	rowBLaterReplyResponseStampedBreach := T.Add(65 * time.Minute)
	rowBLaterReplyResponseStamped := f.seed(t, "row B, later reply, response pre-stamped", f.closedSt.ID, T, now, &resolvedAt1, &closedAt1, nil)
	seedHistory(t, f.ctx, f.ts, rowBLaterReplyResponseStamped.ID, nil, f.newSt.ID, T)
	seedHistory(t, f.ctx, f.ts, rowBLaterReplyResponseStamped.ID, &f.newSt.ID, f.closedSt.ID, T.Add(10*time.Minute))
	seedHistory(t, f.ctx, f.ts, rowBLaterReplyResponseStamped.ID, &f.closedSt.ID, f.inProgressSt.ID, T.Add(60*time.Minute))
	seedHistory(t, f.ctx, f.ts, rowBLaterReplyResponseStamped.ID, &f.inProgressSt.ID, f.resolvedSt.ID, T.Add(120*time.Minute))
	seedHistory(t, f.ctx, f.ts, rowBLaterReplyResponseStamped.ID, &f.resolvedSt.ID, f.closedSt.ID, T.Add(150*time.Minute))
	setSLAFields(t, f.ctx, f.tx, rowBLaterReplyResponseStamped.ID, &resolvedAt1, &rowBLaterReplyResponseStampedAt, nil)
	setSLAResponseBreachedAt(t, f.ctx, f.tx, rowBLaterReplyResponseStamped.ID, &rowBLaterReplyResponseStampedBreach)

	runMigration027And028(t, f.ctx, f.tx)

	// rowA and rowB must end up identical, field for field: C1b's whole
	// point is that an sla_records row already holding SOMETHING must read
	// exactly like one that held nothing at all, once both are compared
	// against the same history.
	recA := f.getRecord(t, rowA.ID)
	recB := f.getRecord(t, rowB.ID)
	require.NotNil(t, recA.ResolvedAt)
	require.True(t, recA.ResolvedAt.Equal(T.Add(10*time.Minute)), "row A: C1 must use the earlier close fact")
	require.NotNil(t, recA.ResolutionElapsedAtMetSeconds)
	require.Equal(t, int64(600), *recA.ResolutionElapsedAtMetSeconds)
	require.Nil(t, recA.ResolutionBreachedAt)
	require.Nil(t, recA.ResponseBreachedAt)
	require.NotNil(t, recA.FirstResponseAt)
	require.True(t, recA.FirstResponseAt.Equal(T.Add(10*time.Minute)))

	require.NotNil(t, recB.ResolvedAt)
	require.True(t, recB.ResolvedAt.Equal(T.Add(10*time.Minute)), "#258: C1b must revise the recorded T+120m down to the earlier T+10m close fact")
	require.NotNil(t, recB.ResolutionElapsedAtMetSeconds)
	require.Equal(t, int64(600), *recB.ResolutionElapsedAtMetSeconds)
	require.Nil(t, recB.ResolutionBreachedAt, "#258: without the fix this would be stamped at T+120m")
	require.Nil(t, recB.ResponseBreachedAt)
	require.NotNil(t, recB.FirstResponseAt)
	require.True(t, recB.FirstResponseAt.Equal(T.Add(10*time.Minute)))

	requireSameTimePtr(t, recA.ResolvedAt, recB.ResolvedAt, "row A and row B ResolvedAt must match field for field")
	requireSameInt64Ptr(t, recA.ResolutionElapsedAtMetSeconds, recB.ResolutionElapsedAtMetSeconds, "ResolutionElapsedAtMetSeconds")
	requireSameTimePtr(t, recA.ResolutionBreachedAt, recB.ResolutionBreachedAt, "ResolutionBreachedAt")
	requireSameTimePtr(t, recA.FirstResponseAt, recB.FirstResponseAt, "FirstResponseAt")
	requireSameInt64Ptr(t, recA.ResponseElapsedAtMetSeconds, recB.ResponseElapsedAtMetSeconds, "ResponseElapsedAtMetSeconds")
	requireSameTimePtr(t, recA.ResponseBreachedAt, recB.ResponseBreachedAt, "ResponseBreachedAt")

	// tickets.resolved_at / closed_at are untouched by C1b: only the SLA
	// record's instant changes (mirroring #247's own pinned assertion).
	tkB := f.getTicket(t, rowB.ID)
	require.NotNil(t, tkB.ResolvedAt)
	require.True(t, tkB.ResolvedAt.Equal(resolvedAt1), "tickets.resolved_at is untouched by C1b")
	require.NotNil(t, tkB.ClosedAt)
	require.True(t, tkB.ClosedAt.Equal(closedAt1))

	// rowB_laterReply: S4a rewinds the real T+70m reply to the corrected
	// T+10m resolution instant and reuses C1b's own 600s — no false response
	// breach survives from the pre-correction reply, which was 40 minutes
	// past the 30-minute target before the fix.
	recBLaterReply := f.getRecord(t, rowBLaterReply.ID)
	require.NotNil(t, recBLaterReply.ResolvedAt)
	require.True(t, recBLaterReply.ResolvedAt.Equal(T.Add(10*time.Minute)))
	require.NotNil(t, recBLaterReply.ResolutionElapsedAtMetSeconds)
	require.Equal(t, int64(600), *recBLaterReply.ResolutionElapsedAtMetSeconds)
	require.Nil(t, recBLaterReply.ResolutionBreachedAt)
	require.NotNil(t, recBLaterReply.FirstResponseAt)
	require.True(t, recBLaterReply.FirstResponseAt.Equal(T.Add(10*time.Minute)),
		"S4a: a real reply that postdates C1b's corrected resolution must be rewound to it")
	require.NotNil(t, recBLaterReply.ResponseElapsedAtMetSeconds)
	require.Equal(t, *recBLaterReply.ResolutionElapsedAtMetSeconds, *recBLaterReply.ResponseElapsedAtMetSeconds,
		"S4a reuses C1b's own frozen number, not an independent recompute")
	require.Nil(t, recBLaterReply.ResponseBreachedAt, "no false response breach left over from the pre-correction T+70m reply")

	// rowB_earlierReply: completely unaffected — the reply already precedes
	// the corrected resolution instant on either side of the fix.
	recBEarlierReply := f.getRecord(t, rowBEarlierReply.ID)
	require.NotNil(t, recBEarlierReply.ResolvedAt)
	require.True(t, recBEarlierReply.ResolvedAt.Equal(T.Add(10*time.Minute)))
	require.NotNil(t, recBEarlierReply.ResolutionElapsedAtMetSeconds)
	require.Equal(t, int64(600), *recBEarlierReply.ResolutionElapsedAtMetSeconds)
	require.NotNil(t, recBEarlierReply.FirstResponseAt)
	require.True(t, recBEarlierReply.FirstResponseAt.Equal(earlyReplyAt), "a reply genuinely earlier than the corrected instant must be left alone")
	require.NotNil(t, recBEarlierReply.ResponseElapsedAtMetSeconds)
	require.Equal(t, int64(300), *recBEarlierReply.ResponseElapsedAtMetSeconds)
	require.Nil(t, recBEarlierReply.ResolutionBreachedAt)
	require.Nil(t, recBEarlierReply.ResponseBreachedAt)

	// rowH_resolveArm: the RESOLVE-facts arm of the identical bug — C1b
	// revises the recorded T+2h re-resolve down to the FIRST resolve, T+10m,
	// the same instant tickets.resolved_at itself already keeps.
	recH := f.getRecord(t, rowH.ID)
	require.NotNil(t, recH.ResolvedAt)
	require.True(t, recH.ResolvedAt.Equal(rowHResolveAt), "C1b must revise the recorded re-resolve down to the FIRST resolve")
	require.NotNil(t, recH.ResolutionElapsedAtMetSeconds)
	require.Equal(t, int64(600), *recH.ResolutionElapsedAtMetSeconds)
	require.Nil(t, recH.ResolutionBreachedAt, "without the fix this would be stamped late at T+2h")
	require.Nil(t, recH.ResponseBreachedAt)
	require.NotNil(t, recH.FirstResponseAt)
	require.True(t, recH.FirstResponseAt.Equal(rowHResolveAt))
	tkH := f.getTicket(t, rowH.ID)
	require.NotNil(t, tkH.ResolvedAt)
	require.True(t, tkH.ResolvedAt.Equal(rowHResolveAt), "tickets.resolved_at already kept the first resolve; untouched by C1b")

	// ctlAlreadyEarliestOnTime: a stored resolved_at that is already the
	// earliest fact must not move — catches a revision in the wrong
	// direction (a bug that would make the strict `<` a `>` or `<=` by
	// mistake).
	recCtlOnTime := f.getRecord(t, ctlAlreadyEarliestOnTime.ID)
	require.NotNil(t, recCtlOnTime.ResolvedAt)
	require.True(t, recCtlOnTime.ResolvedAt.Equal(ctlOnTimeResolveAt))
	require.NotNil(t, recCtlOnTime.ResolutionElapsedAtMetSeconds)
	require.Equal(t, int64(600), *recCtlOnTime.ResolutionElapsedAtMetSeconds)
	require.Nil(t, recCtlOnTime.ResolutionBreachedAt)
	require.Nil(t, recCtlOnTime.ResponseBreachedAt)

	// ctlAlreadyEarliestLate: a genuinely late recorded resolve that is
	// already the earliest fact must still stamp normally.
	recCtlLate := f.getRecord(t, ctlAlreadyEarliestLate.ID)
	require.NotNil(t, recCtlLate.ResolvedAt)
	require.True(t, recCtlLate.ResolvedAt.Equal(ctlLateResolveAt))
	require.NotNil(t, recCtlLate.ResolutionElapsedAtMetSeconds)
	require.Equal(t, int64(3600), *recCtlLate.ResolutionElapsedAtMetSeconds)
	require.NotNil(t, recCtlLate.ResolutionBreachedAt, "a genuinely late recorded resolve, already the earliest, must still stamp")
	require.True(t, recCtlLate.ResolutionBreachedAt.Equal(ctlLateResolveAt))
	require.NotNil(t, recCtlLate.ResponseBreachedAt)
	require.True(t, recCtlLate.ResponseBreachedAt.Equal(ctlLateResolveAt))

	// ctlResolvedGatedOff / ctlReopenedGatedOff: the close arm stays gated
	// off for a ticket that is not currently Closed, exactly as it is for
	// C1 — C1b's revision never reaches either row.
	recCtlResolvedGated := f.getRecord(t, ctlResolvedGatedOff.ID)
	require.NotNil(t, recCtlResolvedGated.ResolvedAt)
	require.True(t, recCtlResolvedGated.ResolvedAt.Equal(ctlGatedResolveAt), "#238 sibling: close arm gated off for a Resolved ticket")
	require.NotNil(t, recCtlResolvedGated.ResolutionBreachedAt)
	require.NotNil(t, recCtlResolvedGated.ResponseBreachedAt)

	recCtlReopenedGated := f.getRecord(t, ctlReopenedGatedOff.ID)
	require.NotNil(t, recCtlReopenedGated.ResolvedAt)
	require.True(t, recCtlReopenedGated.ResolvedAt.Equal(ctlGatedResolveAt), "close arm gated off for a reopened, non-Closed ticket")
	require.NotNil(t, recCtlReopenedGated.ResolutionBreachedAt)
	require.NotNil(t, recCtlReopenedGated.ResponseBreachedAt)

	// ctlPreStampedRecorded (#259): C1b revises resolved_at/the frozen number
	// exactly as it would for an unrecorded row, and NEITHER pre-existing
	// stamp is contradicted, cleared, or moved. S4 fills first_response_at
	// (still NULL going in) from C1b's own corrected resolved_at, and S5
	// freezes response_elapsed_at_met_seconds from that — response_breached_at
	// stays exactly where the (simulated) beta sweep put it, since S6 only
	// stamps a still-NULL column.
	recCtlPreStampedRecorded := f.getRecord(t, ctlPreStampedRecorded.ID)
	require.NotNil(t, recCtlPreStampedRecorded.ResolvedAt)
	require.True(t, recCtlPreStampedRecorded.ResolvedAt.Equal(T.Add(10*time.Minute)), "#259: an existing stamp must not block C1b's revision")
	require.NotNil(t, recCtlPreStampedRecorded.ResolutionElapsedAtMetSeconds)
	require.Equal(t, int64(600), *recCtlPreStampedRecorded.ResolutionElapsedAtMetSeconds)
	require.NotNil(t, recCtlPreStampedRecorded.ResolutionBreachedAt)
	require.True(t, recCtlPreStampedRecorded.ResolutionBreachedAt.Equal(ctlPreStampedAt), "an existing stamp must never move")
	require.NotNil(t, recCtlPreStampedRecorded.FirstResponseAt)
	require.True(t, recCtlPreStampedRecorded.FirstResponseAt.Equal(T.Add(10*time.Minute)), "S4's copy of C1b's corrected resolution")
	require.NotNil(t, recCtlPreStampedRecorded.ResponseElapsedAtMetSeconds)
	require.Equal(t, int64(600), *recCtlPreStampedRecorded.ResponseElapsedAtMetSeconds)
	require.NotNil(t, recCtlPreStampedRecorded.ResponseBreachedAt)
	require.True(t, recCtlPreStampedRecorded.ResponseBreachedAt.Equal(ctlPreStampedAt), "an existing response stamp must never move either")

	// ctlPreStampedUnrecorded (#259, the D1/D2 regression): must come out
	// IDENTICAL to ctlPreStampedRecorded on every field, field for field.
	// Before the fix, this fails: this row resolves via C1 (never gated on a
	// stamp) to T+10m, while ctlPreStampedRecorded's C1b was blocked by the
	// stamp gate and stuck at T+120m.
	recCtlPreStampedUnrecorded := f.getRecord(t, ctlPreStampedUnrecorded.ID)
	requireSameTimePtr(t, recCtlPreStampedRecorded.ResolvedAt, recCtlPreStampedUnrecorded.ResolvedAt, "#259: a pre-recorded row and an unrecorded row with identical history and stamps must resolve identically")
	requireSameInt64Ptr(t, recCtlPreStampedRecorded.ResolutionElapsedAtMetSeconds, recCtlPreStampedUnrecorded.ResolutionElapsedAtMetSeconds, "ResolutionElapsedAtMetSeconds")
	requireSameTimePtr(t, recCtlPreStampedRecorded.ResolutionBreachedAt, recCtlPreStampedUnrecorded.ResolutionBreachedAt, "ResolutionBreachedAt")
	requireSameTimePtr(t, recCtlPreStampedRecorded.FirstResponseAt, recCtlPreStampedUnrecorded.FirstResponseAt, "FirstResponseAt")
	requireSameInt64Ptr(t, recCtlPreStampedRecorded.ResponseElapsedAtMetSeconds, recCtlPreStampedUnrecorded.ResponseElapsedAtMetSeconds, "ResponseElapsedAtMetSeconds")
	requireSameTimePtr(t, recCtlPreStampedRecorded.ResponseBreachedAt, recCtlPreStampedUnrecorded.ResponseBreachedAt, "ResponseBreachedAt")

	// rowBLaterReplyResponseStamped (#259 part 2): C1b revises the resolution
	// side exactly as rowBLaterReply does (no resolution stamp involved), S4a
	// rewinds the reply and reuses C1b's own 600s, and the pre-existing
	// response_breached_at stamp is left exactly where it was — this already
	// passes today, documenting that the response side follows the same
	// no-stamp-gate rule as the resolution side.
	recRowBLaterReplyResponseStamped := f.getRecord(t, rowBLaterReplyResponseStamped.ID)
	require.NotNil(t, recRowBLaterReplyResponseStamped.ResolvedAt)
	require.True(t, recRowBLaterReplyResponseStamped.ResolvedAt.Equal(T.Add(10*time.Minute)))
	require.NotNil(t, recRowBLaterReplyResponseStamped.ResolutionElapsedAtMetSeconds)
	require.Equal(t, int64(600), *recRowBLaterReplyResponseStamped.ResolutionElapsedAtMetSeconds)
	require.Nil(t, recRowBLaterReplyResponseStamped.ResolutionBreachedAt)
	require.NotNil(t, recRowBLaterReplyResponseStamped.FirstResponseAt)
	require.True(t, recRowBLaterReplyResponseStamped.FirstResponseAt.Equal(T.Add(10*time.Minute)), "S4a rewinds the reply to C1b's corrected resolution")
	require.NotNil(t, recRowBLaterReplyResponseStamped.ResponseElapsedAtMetSeconds)
	require.Equal(t, int64(600), *recRowBLaterReplyResponseStamped.ResponseElapsedAtMetSeconds, "S4a reuses C1b's own frozen number")
	require.NotNil(t, recRowBLaterReplyResponseStamped.ResponseBreachedAt)
	require.True(t, recRowBLaterReplyResponseStamped.ResponseBreachedAt.Equal(rowBLaterReplyResponseStampedBreach), "an existing response stamp must never move")
}

// TestMigration_028SecondRunChangesNothing pins #246: running migration
// 000028's up.sql a second time against data the FIRST run already committed
// must be a complete no-op — the more likely re-run path in practice is not
// `migrate force` but `migrate down 1` (000028's down is comment-only)
// followed by `up`, which re-executes this exact file against already-repaired
// data. Before the fix, the second run's S2/S5 would freeze the elapsed
// number for every row the first run had deliberately left estimated (and
// therefore unfrozen), and S3/S6 would then stamp a false breach from that
// newly-frozen number.
func TestMigration_028SecondRunChangesNothing(t *testing.T) {
	f := newMigration028Fixture(t, "second-run")
	now := time.Now().UTC().Truncate(time.Millisecond)
	T := now.Add(-6 * time.Hour)
	lateAt := T.Add(3 * time.Hour) // 180min, past the 30-minute target
	onTimeAt := T.Add(10 * time.Minute)

	// closedNoHistory / resolvedNoHistory: estimated, late — no fact anywhere,
	// C2 estimates from updated_at.
	closedNoHistory := f.seed(t, "closed no history", f.closedSt.ID, T, lateAt, nil, nil, nil)
	resolvedNoHistory := f.seed(t, "resolved no history", f.resolvedSt.ID, T, lateAt, nil, nil, nil)

	// estimatedWithRealLateResponse: estimated resolution, but a REAL late
	// first_response_at already on the sla_records row before either
	// migration runs.
	realFirstResponseAt := T.Add(2 * time.Hour)
	estimatedWithRealLateResponse := f.seed(t, "closed no history, real late response", f.closedSt.ID, T, lateAt, nil, nil, nil)
	_, err := f.tx.ExecContext(f.ctx,
		`UPDATE sla_records SET first_response_at = $2 WHERE ticket_id = $1`,
		estimatedWithRealLateResponse.ID, realFirstResponseAt)
	require.NoError(t, err)

	// closedAfterLateUnstampedResolve: a genuine late FACT, recovered from
	// history — must stamp on run 1, and the stamp must survive run 2
	// untouched.
	closedAfterLateUnstampedResolve := f.seed(t, "closed after late unstamped resolve", f.closedSt.ID, T, now, nil, nil, nil)
	seedHistory(t, f.ctx, f.ts, closedAfterLateUnstampedResolve.ID, nil, f.newSt.ID, T)
	seedHistory(t, f.ctx, f.ts, closedAfterLateUnstampedResolve.ID, &f.newSt.ID, f.resolvedSt.ID, T.Add(1*time.Hour))
	seedHistory(t, f.ctx, f.ts, closedAfterLateUnstampedResolve.ID, &f.resolvedSt.ID, f.closedSt.ID, T.Add(2*time.Hour))

	// onTimeFact: currently Resolved, on-time fact — frozen, no stamp, must
	// stay that way.
	onTimeFactAt := onTimeAt
	onTimeFact := f.seed(t, "on time fact", f.resolvedSt.ID, T, now, &onTimeFactAt, nil, nil)

	// reopenedOnTime (#244 shape): In Progress, resolved_at on time, no
	// history — both targets met and frozen, nothing outstanding.
	reopenedAt := onTimeAt
	reopenedOnTime := f.seed(t, "reopened on time", f.inProgressSt.ID, T, now, &reopenedAt, nil, nil)

	// preFrozenBy000027: simulates a row an EARLIER deployment's 000027 had
	// already frozen (sla_records.resolved_at set before either migration in
	// this test runs) — late, so 000028's S3 must stamp it on run 1, and that
	// stamp must survive run 2 untouched.
	preFrozenAt := T.Add(1 * time.Hour) // late
	preFrozenBy000027 := f.seed(t, "pre-frozen by 000027", f.resolvedSt.ID, T, now, &preFrozenAt, nil, nil)
	_, err = f.tx.ExecContext(f.ctx,
		`UPDATE sla_records SET resolved_at = $2 WHERE ticket_id = $1`,
		preFrozenBy000027.ID, preFrozenAt)
	require.NoError(t, err)

	// The #247 close→reopen→resolve→close row.
	closeReopenResolveClose := f.seed(t, "close reopen resolve close", f.closedSt.ID, T, now, nil, nil, nil)
	seedHistory(t, f.ctx, f.ts, closeReopenResolveClose.ID, nil, f.newSt.ID, T)
	seedHistory(t, f.ctx, f.ts, closeReopenResolveClose.ID, &f.newSt.ID, f.closedSt.ID, T.Add(10*time.Minute))
	seedHistory(t, f.ctx, f.ts, closeReopenResolveClose.ID, &f.closedSt.ID, f.inProgressSt.ID, T.Add(60*time.Minute))
	seedHistory(t, f.ctx, f.ts, closeReopenResolveClose.ID, &f.inProgressSt.ID, f.resolvedSt.ID, T.Add(120*time.Minute))
	seedHistory(t, f.ctx, f.ts, closeReopenResolveClose.ID, &f.resolvedSt.ID, f.closedSt.ID, T.Add(150*time.Minute))

	// rowBRecorded (#258): the identical close→reopen→resolve→close history,
	// but with sla_records.resolved_at PRE-RECORDED at the later re-resolve
	// (T+120m) — the shape C1b exists to revise. On run 1, C1b must rewrite
	// it down to T+10m. This fixture is the reason the test grows a
	// sla_paused_seconds bump below: it is the one row in this test whose
	// frozen number C1b computes fresh on run 1, so it is the row that would
	// actually catch a `<=` bug that let a rerun recompute and drift it.
	rowBRecordedResolvedAt := T.Add(120 * time.Minute)
	rowBRecordedClosedAt := T.Add(150 * time.Minute)
	rowBRecorded := f.seed(t, "row B recorded, close reopen resolve close", f.closedSt.ID, T, now,
		&rowBRecordedResolvedAt, &rowBRecordedClosedAt, nil)
	seedHistory(t, f.ctx, f.ts, rowBRecorded.ID, nil, f.newSt.ID, T)
	seedHistory(t, f.ctx, f.ts, rowBRecorded.ID, &f.newSt.ID, f.closedSt.ID, T.Add(10*time.Minute))
	seedHistory(t, f.ctx, f.ts, rowBRecorded.ID, &f.closedSt.ID, f.inProgressSt.ID, T.Add(60*time.Minute))
	seedHistory(t, f.ctx, f.ts, rowBRecorded.ID, &f.inProgressSt.ID, f.resolvedSt.ID, T.Add(120*time.Minute))
	seedHistory(t, f.ctx, f.ts, rowBRecorded.ID, &f.resolvedSt.ID, f.closedSt.ID, T.Add(150*time.Minute))
	setSLAFields(t, f.ctx, f.tx, rowBRecorded.ID, &rowBRecordedResolvedAt, nil, nil)

	// rowHRecorded (#258, resolve arm): currently Resolved, kept its FIRST
	// resolve (T+10m) on the tickets row, but sla_records.resolved_at is
	// pre-recorded at the later re-resolve (T+2h).
	rowHResolveAt := T.Add(10 * time.Minute)
	rowHReResolveAt := T.Add(2 * time.Hour)
	rowHRecorded := f.seed(t, "row H recorded, resolved twice", f.resolvedSt.ID, T, now, &rowHResolveAt, nil, nil)
	seedHistory(t, f.ctx, f.ts, rowHRecorded.ID, nil, f.newSt.ID, T)
	seedHistory(t, f.ctx, f.ts, rowHRecorded.ID, &f.newSt.ID, f.resolvedSt.ID, rowHResolveAt)
	seedHistory(t, f.ctx, f.ts, rowHRecorded.ID, &f.resolvedSt.ID, f.resolvedSt.ID, rowHReResolveAt)
	setSLAFields(t, f.ctx, f.tx, rowHRecorded.ID, &rowHReResolveAt, nil, nil)

	// rowBRecordedPreStamped (#259): rowBRecorded's exact shape, plus both
	// breach stamps pre-set at T+60m. Before #259's fix, the stamp gate on
	// C1b left this row stuck at T+120m forever, which incidentally made it
	// LOOK idempotent across two runs — this row exists so removing the gate
	// is checked against a rerun too, not only a single run: a stamped row
	// revised on run 1 must not change AGAIN on run 2 (the strict `<` no
	// longer matches once resolved_at equals the earliest fact), and neither
	// stamp may ever move, on either run.
	rowBRecordedPreStampedAt := T.Add(60 * time.Minute)
	rowBRecordedPreStamped := f.seed(t, "row B recorded, pre-stamped, close reopen resolve close", f.closedSt.ID, T, now,
		&rowBRecordedResolvedAt, &rowBRecordedClosedAt, nil)
	seedHistory(t, f.ctx, f.ts, rowBRecordedPreStamped.ID, nil, f.newSt.ID, T)
	seedHistory(t, f.ctx, f.ts, rowBRecordedPreStamped.ID, &f.newSt.ID, f.closedSt.ID, T.Add(10*time.Minute))
	seedHistory(t, f.ctx, f.ts, rowBRecordedPreStamped.ID, &f.closedSt.ID, f.inProgressSt.ID, T.Add(60*time.Minute))
	seedHistory(t, f.ctx, f.ts, rowBRecordedPreStamped.ID, &f.inProgressSt.ID, f.resolvedSt.ID, T.Add(120*time.Minute))
	seedHistory(t, f.ctx, f.ts, rowBRecordedPreStamped.ID, &f.resolvedSt.ID, f.closedSt.ID, T.Add(150*time.Minute))
	setSLAFields(t, f.ctx, f.tx, rowBRecordedPreStamped.ID, &rowBRecordedResolvedAt, nil, &rowBRecordedPreStampedAt)
	setSLAResponseBreachedAt(t, f.ctx, f.tx, rowBRecordedPreStamped.ID, &rowBRecordedPreStampedAt)

	ids := []uuid.UUID{
		closedNoHistory.ID, resolvedNoHistory.ID, estimatedWithRealLateResponse.ID,
		closedAfterLateUnstampedResolve.ID, onTimeFact.ID, reopenedOnTime.ID,
		preFrozenBy000027.ID, closeReopenResolveClose.ID,
		rowBRecorded.ID, rowHRecorded.ID, rowBRecordedPreStamped.ID,
	}

	runMigration027And028(t, f.ctx, f.tx)

	type snapshot struct {
		rec        sla.Record
		statusID   uuid.UUID
		resolvedAt *time.Time
		closedAt   *time.Time
	}
	snap := func() map[uuid.UUID]snapshot {
		out := make(map[uuid.UUID]snapshot, len(ids))
		for _, id := range ids {
			rec := f.getRecord(t, id)
			tk := f.getTicket(t, id)
			out[id] = snapshot{rec: rec, statusID: tk.StatusID, resolvedAt: tk.ResolvedAt, closedAt: tk.ClosedAt}
		}
		return out
	}

	first := snap()

	// Explicit Nil asserts after run 1, so a failure below is readable rather
	// than buried in a map-equality diff.
	require.Nil(t, first[closedNoHistory.ID].rec.ResolutionBreachedAt)
	require.Nil(t, first[closedNoHistory.ID].rec.ResponseBreachedAt)
	require.Nil(t, first[closedNoHistory.ID].rec.ResolutionElapsedAtMetSeconds)
	require.Nil(t, first[resolvedNoHistory.ID].rec.ResolutionBreachedAt)
	require.Nil(t, first[resolvedNoHistory.ID].rec.ResponseBreachedAt)
	require.NotNil(t, first[closedAfterLateUnstampedResolve.ID].rec.ResolutionBreachedAt, "a real late fact must be stamped on run 1")
	require.NotNil(t, first[preFrozenBy000027.ID].rec.ResolutionBreachedAt, "a row 000027 already froze must still be stamped by 028's S3 on run 1")

	// #258: C1b must have already revised both rows down to their earliest
	// fact on run 1, exactly as TestMigration_SLABackfillRevisesRecordedResolutionToEarlierFact
	// pins in isolation.
	require.NotNil(t, first[rowBRecorded.ID].rec.ResolvedAt)
	require.True(t, first[rowBRecorded.ID].rec.ResolvedAt.Equal(T.Add(10*time.Minute)), "run 1: C1b must revise rowBRecorded down to its earliest (close) fact")
	require.NotNil(t, first[rowBRecorded.ID].rec.ResolutionElapsedAtMetSeconds)
	require.Equal(t, int64(600), *first[rowBRecorded.ID].rec.ResolutionElapsedAtMetSeconds)
	require.Nil(t, first[rowBRecorded.ID].rec.ResolutionBreachedAt)
	require.NotNil(t, first[rowHRecorded.ID].rec.ResolvedAt)
	require.True(t, first[rowHRecorded.ID].rec.ResolvedAt.Equal(rowHResolveAt), "run 1: C1b must revise rowHRecorded down to its FIRST resolve")
	require.Nil(t, first[rowHRecorded.ID].rec.ResolutionBreachedAt)

	// #259: C1b must revise rowBRecordedPreStamped exactly like rowBRecorded
	// on run 1, and neither pre-existing stamp is contradicted or moved.
	require.NotNil(t, first[rowBRecordedPreStamped.ID].rec.ResolvedAt)
	require.True(t, first[rowBRecordedPreStamped.ID].rec.ResolvedAt.Equal(T.Add(10*time.Minute)), "run 1: an existing stamp must not block C1b's revision")
	require.NotNil(t, first[rowBRecordedPreStamped.ID].rec.ResolutionElapsedAtMetSeconds)
	require.Equal(t, int64(600), *first[rowBRecordedPreStamped.ID].rec.ResolutionElapsedAtMetSeconds)
	require.NotNil(t, first[rowBRecordedPreStamped.ID].rec.ResolutionBreachedAt)
	require.True(t, first[rowBRecordedPreStamped.ID].rec.ResolutionBreachedAt.Equal(rowBRecordedPreStampedAt), "run 1: an existing stamp must never move")
	require.NotNil(t, first[rowBRecordedPreStamped.ID].rec.ResponseBreachedAt)
	require.True(t, first[rowBRecordedPreStamped.ID].rec.ResponseBreachedAt.Equal(rowBRecordedPreStampedAt), "run 1: an existing response stamp must never move")

	// #258: grow rowBRecorded's sla_paused_seconds between the two runs —
	// this is the strictness check the design's test plan calls for. With
	// C1b's idempotence predicate correctly STRICT (f.at < r.resolved_at), a
	// second run must not touch this row at all, so this growth must never
	// reach its frozen elapsed reading. With a non-strict `<=` instead, the
	// second run would recompute resolution_elapsed_at_met_seconds using this
	// grown total and drift it down from 600 to 300 — the exact regression
	// the first==second comparison below exists to catch.
	_, err = f.tx.ExecContext(f.ctx, `UPDATE tickets SET sla_paused_seconds = sla_paused_seconds + 300 WHERE id = $1`, rowBRecorded.ID)
	require.NoError(t, err)

	// Run 000028's down (comment-only — a no-op) and then its up again,
	// against the SAME committed data, pinning the down/up re-run path.
	execMigrationFile(t, f.ctx, f.tx, "migrations/000028_repair_resolved_status_invariant.down.sql", nil)
	execMigrationFile(t, f.ctx, f.tx, "migrations/000028_repair_resolved_status_invariant.up.sql", nil)

	second := snap()

	require.Equal(t, first, second, "a second run of migration 000028 against already-committed data must change nothing (#246, #258)")

	// Explicit Nil asserts on both breach columns of the estimated rows after
	// run 2 as well: the failure mode this test exists to catch is exactly
	// these flipping from Nil to non-Nil.
	require.Nil(t, second[closedNoHistory.ID].rec.ResolutionBreachedAt)
	require.Nil(t, second[closedNoHistory.ID].rec.ResponseBreachedAt)
	require.Nil(t, second[closedNoHistory.ID].rec.ResolutionElapsedAtMetSeconds)
	require.Nil(t, second[resolvedNoHistory.ID].rec.ResolutionBreachedAt)
	require.Nil(t, second[resolvedNoHistory.ID].rec.ResponseBreachedAt)
	require.Nil(t, second[estimatedWithRealLateResponse.ID].rec.ResolutionBreachedAt)

	// #258: explicit Nil asserts for rowBRecorded/rowHRecorded after run 2 as
	// well — the failure mode this addition exists to catch is a false
	// breach stamp appearing on the SECOND run only, from a re-drifted
	// frozen number the strict `<` was supposed to prevent.
	require.Nil(t, second[rowBRecorded.ID].rec.ResolutionBreachedAt)
	require.NotNil(t, second[rowBRecorded.ID].rec.ResolutionElapsedAtMetSeconds)
	require.Equal(t, int64(600), *second[rowBRecorded.ID].rec.ResolutionElapsedAtMetSeconds,
		"#258: the frozen number must not drift after sla_paused_seconds grew between the two runs")
	require.Nil(t, second[rowHRecorded.ID].rec.ResolutionBreachedAt)

	// #259: rowBRecordedPreStamped's stamps must still not have moved after
	// run 2 — the map-equality check above already covers this via
	// first == second, but this spells out the specific regression a stamp
	// gate coming back would cause: run 2 blocked (or worse, re-triggered) by
	// its own presence.
	require.NotNil(t, second[rowBRecordedPreStamped.ID].rec.ResolutionBreachedAt)
	require.True(t, second[rowBRecordedPreStamped.ID].rec.ResolutionBreachedAt.Equal(rowBRecordedPreStampedAt), "run 2: an existing stamp must never move")
	require.NotNil(t, second[rowBRecordedPreStamped.ID].rec.ResponseBreachedAt)
	require.True(t, second[rowBRecordedPreStamped.ID].rec.ResponseBreachedAt.Equal(rowBRecordedPreStampedAt), "run 2: an existing response stamp must never move")
}

// TestMigration_RewindThrough000027LosesEstimatedMarkerAndReStamps pins #249:
// the NULL resolution_elapsed_at_met_seconds marker #246 relies on to tell a
// later run "this row was only ever estimated" survives a rewind that stops
// at 000028 (TestMigration_028SecondRunChangesNothing above), but NOT one
// that goes down through 000027 itself. 000027's down migration drops
// resolution_elapsed_at_met_seconds/response_elapsed_at_met_seconds
// entirely — the only place the fact/estimate distinction was recorded —
// so once it is gone, 000027's own up-migration backfill unconditionally
// re-freezes elapsed from whatever sla_records.resolved_at/first_response_at
// already hold, and the re-frozen number is indistinguishable from a fact to
// 000028's S3/S6, which stamp a breach from it. This is not fixable in SQL:
// the distinguishing information is genuinely gone, not merely unread. This
// test pins the known, accepted result — a re-stamp — as a deliberate,
// documented trade-off rather than a regression that could slip in silently.
// See the LIMITS block in 000028_repair_resolved_status_invariant.up.sql.
func TestMigration_RewindThrough000027LosesEstimatedMarkerAndReStamps(t *testing.T) {
	f := newMigration028Fixture(t, "rewind-027")
	now := time.Now().UTC().Truncate(time.Millisecond)
	T := now.Add(-4 * time.Hour)
	lateAt := T.Add(3 * time.Hour) // 180min, past the 30-minute target

	// The standard estimated shape: Closed, both timestamps NULL, no history —
	// no fact anywhere, updated_at is the only recoverable instant.
	tk := f.seed(t, "closed no history, rewound", f.closedSt.ID, T, lateAt, nil, nil, nil)

	// Run 1: 000027 up (columns already exist, its ALTER TABLE is skipped) +
	// 000028 up, exactly as TestMigration_EstimatedResolutionIsRecordedButNeitherFrozenNorStamped
	// exercises: recorded, but neither frozen nor stamped.
	runMigration027And028(t, f.ctx, f.tx)

	rec1 := f.getRecord(t, tk.ID)
	require.NotNil(t, rec1.ResolvedAt)
	require.True(t, rec1.ResolvedAt.Equal(lateAt))
	require.Nil(t, rec1.ResolutionElapsedAtMetSeconds, "estimated: never frozen on the first run")
	require.Nil(t, rec1.ResolutionBreachedAt, "estimated: never stamped on the first run")
	require.NotNil(t, rec1.FirstResponseAt)
	require.True(t, rec1.FirstResponseAt.Equal(lateAt))
	require.Nil(t, rec1.ResponseElapsedAtMetSeconds)
	require.Nil(t, rec1.ResponseBreachedAt)

	// The rewind from the issue: 000028 down (comment-only, a no-op) ->
	// 000027 down (drops the two columns, taking the fact/estimate
	// distinction with them) -> 000027 up again — this time NOT skipping its
	// ALTER TABLE, since the columns are actually gone and it must recreate
	// them -> 000028 up again.
	execMigrationFile(t, f.ctx, f.tx, "migrations/000028_repair_resolved_status_invariant.down.sql", nil)
	execMigrationFile(t, f.ctx, f.tx, "migrations/000027_sla_frozen_elapsed.down.sql", nil)
	execMigrationFile(t, f.ctx, f.tx, "migrations/000027_sla_frozen_elapsed.up.sql", nil)
	execMigrationFile(t, f.ctx, f.tx, "migrations/000028_repair_resolved_status_invariant.up.sql", nil)

	// The accepted, documented result #249 pins: 000027's re-run backfill
	// re-froze elapsed from the estimated instant sla_records already held
	// (now indistinguishable from a fact, the column having been dropped and
	// recreated), and 000028's S3/S6 then stamp a breach from it — for a
	// resolution that was only ever an estimate.
	rec2 := f.getRecord(t, tk.ID)
	require.NotNil(t, rec2.ResolutionElapsedAtMetSeconds,
		"#249: a rewind through 000027 itself re-freezes the estimated instant as if it were a fact")
	require.NotNil(t, rec2.ResolutionBreachedAt,
		"#249: accepted re-stamp — the fact/estimate distinction cannot survive a rewind through 000027 itself")
	require.True(t, rec2.ResolutionBreachedAt.Equal(lateAt))
	require.NotNil(t, rec2.ResponseElapsedAtMetSeconds, "#249: same, response side")
	require.NotNil(t, rec2.ResponseBreachedAt, "#249: same, response side")
	require.True(t, rec2.ResponseBreachedAt.Equal(lateAt))
}
