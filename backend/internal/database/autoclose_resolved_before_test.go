package database_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// ListResolvedTicketsBefore is only really tested against Postgres: the bug
// (#191) is entirely in the SQL predicate, not in Go, so a fake store proves
// nothing about it.
//
// This runs the real query against a row that satisfies resolved_at < cutoff
// but sits in a status other than Resolved — exactly the shape of a legacy
// row that, pre-fix, was listed on every sweep, locked, skipped, and listed
// again forever.
func TestTicketStore_ListResolvedBefore_ExcludesWrongStatus(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()

	us := userstore.New(q)
	cs := categorystore.New(q)
	ts := ticketstore.New(q)

	reporter := user.User{
		ID: uuid.New(), Email: "resolved-before-" + uuid.NewString() + "@test.local",
		DisplayName: "Reporter", Role: user.RoleUser,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, reporter))
	cat := category.Category{ID: uuid.New(), Name: "RB " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))

	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)
	resolvedSt, err := ts.GetStatusByName(ctx, ticket.StatusNameResolved)
	require.NoError(t, err)
	closedSt, err := ts.GetStatusByName(ctx, ticket.StatusNameClosed)
	require.NoError(t, err)

	old := time.Now().UTC().Add(-10 * 24 * time.Hour).Truncate(time.Millisecond)
	mk := func(subject string, statusID uuid.UUID, resolvedAt *time.Time) ticket.Ticket {
		tk := ticket.Ticket{
			ID:             uuid.New(),
			TrackingNumber: ticket.TrackingNumber("RB-" + uuid.NewString()[:8]),
			Subject:        subject,
			Description:    subject,
			CategoryID:     cat.ID,
			Priority:       ticket.PriorityMedium,
			StatusID:       statusID,
			ReporterUserID: &reporter.ID,
			ResolvedAt:     resolvedAt,
			CreatedAt:      old.Add(-24 * time.Hour),
			UpdatedAt:      old,
		}
		require.NoError(t, ts.Create(ctx, tk))
		// Create never sets resolved_at/closed_at (a new ticket cannot start
		// resolved) — this test needs a row that already carries one, which is
		// what a status change writes, so it goes through Update the same way.
		require.NoError(t, ts.Update(ctx, tk))
		return tk
	}

	// Genuinely eligible: in Resolved, resolved_at in the past.
	eligible := mk("RB eligible", resolvedSt.ID, &old)
	// Legacy row: resolved_at set, but moved back to New without clearing it.
	legacyOpen := mk("RB legacy open", newSt.ID, &old)
	// Legacy row: resolved_at stale, ticket already Closed.
	legacyClosed := mk("RB legacy closed", closedSt.ID, &old)

	got, err := ts.ListResolvedBefore(ctx, time.Now(), resolvedSt.ID, 500)
	require.NoError(t, err)

	ids := map[uuid.UUID]bool{}
	for _, tk := range got {
		ids[tk.ID] = true
	}
	require.True(t, ids[eligible.ID], "a ticket genuinely in Resolved past the cutoff must be listed")
	require.False(t, ids[legacyOpen.ID], "a row not in Resolved must never be listed, even with a stale resolved_at")
	require.False(t, ids[legacyClosed.ID], "a Closed ticket with a stale resolved_at must never be listed")
}

// seedTicketState directly overwrites the fields a real status transition
// would change — status_id, resolved_at, closed_at, pending_since,
// sla_paused_seconds — AND updated_at, exactly as given in tk.
// ticketstore.Store.Update always stamps updated_at with time.Now() (correct
// for the real app: every status door really does touch the row at the
// instant it runs), which defeats a test that needs to control updated_at
// precisely — to prove a migration's fallback reads THIS stored value, not
// whatever moment the test happened to execute. Bypasses the store layer
// deliberately, only for that one column; every other field still goes
// through the same values Update would have written.
func seedTicketState(t *testing.T, ctx context.Context, tx *sql.Tx, tk ticket.Ticket) {
	t.Helper()
	_, err := tx.ExecContext(ctx, `
		UPDATE tickets
		SET status_id = $2, resolved_at = $3, closed_at = $4,
		    pending_since = $5, sla_paused_seconds = $6, updated_at = $7
		WHERE id = $1`,
		tk.ID, tk.StatusID, tk.ResolvedAt, tk.ClosedAt, tk.PendingSince, tk.SLAPausedSeconds, tk.UpdatedAt)
	require.NoError(t, err)
}

// seedHistory writes one ticket_status_history row directly with an
// explicit created_at, for tests that need to control the transition instant
// exactly — CreateStatusHistoryEntry writes created_at as given, unlike a
// real transition through Service, which always stamps time.Now().
func seedHistory(t *testing.T, ctx context.Context, ts *ticketstore.Store, ticketID uuid.UUID, from *uuid.UUID, to uuid.UUID, at time.Time) {
	t.Helper()
	require.NoError(t, ts.CreateStatusHistoryEntry(ctx, ticket.StatusHistoryEntry{
		ID:           uuid.New(),
		TicketID:     ticketID,
		FromStatusID: from,
		ToStatusID:   to,
		CreatedAt:    at,
	}))
}

// TestMigration_RepairsResolvedStatusInvariant runs migration 000028's exact
// SQL (read from disk, not reimplemented) against rows seeded directly to
// violate the invariant it repairs:
//
//	| current status   | resolved_at               | closed_at |
//	|-------------------|---------------------------|-----------|
//	| system Resolved   | set                       | NULL      |
//	| system Closed     | kept as is (set or NULL)  | set       |
//	| anything else     | NULL                      | NULL      |
//
// and checks it fixes them, and only them. Round 4 adversarial review
// (#237-241) added the In Progress/Pending/custom-status cases (#237), the
// stale-closed_at-on-a-reopened-ticket case (#239), and the
// history-recovered resolved_at/closed_at cases (#238 and #241, including
// the duplicate-row and inconsistent-history edge cases the recovery rule
// exists to handle).
func TestMigration_RepairsResolvedStatusInvariant(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()

	tx, err := db.SQL.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	ctx := context.Background()
	q := dbgen.New(tx)

	us := userstore.New(q)
	cs := categorystore.New(q)
	ts := ticketstore.New(q)

	reporter := user.User{
		ID: uuid.New(), Email: "migration-" + uuid.NewString() + "@test.local",
		DisplayName: "Reporter", Role: user.RoleUser,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, reporter))
	cat := category.Category{ID: uuid.New(), Name: "Mig " + uuid.NewString()[:8], SortOrder: 1, Active: true}
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

	var customStatusID uuid.UUID
	require.NoError(t, tx.QueryRowContext(ctx,
		`INSERT INTO statuses (name, kind, sort_order, color) VALUES ($1, 'custom', 50, '#000000') RETURNING id`,
		"Mig custom "+uuid.NewString()[:8]).Scan(&customStatusID))

	stale := time.Now().UTC().Add(-30 * 24 * time.Hour).Truncate(time.Millisecond)
	mk := func(subject string, statusID uuid.UUID, resolvedAt, closedAt *time.Time) ticket.Ticket {
		tk := ticket.Ticket{
			ID:             uuid.New(),
			TrackingNumber: ticket.TrackingNumber("MIG-" + uuid.NewString()[:8]),
			Subject:        subject,
			Description:    subject,
			CategoryID:     cat.ID,
			Priority:       ticket.PriorityMedium,
			StatusID:       statusID,
			ReporterUserID: &reporter.ID,
			ResolvedAt:     resolvedAt,
			ClosedAt:       closedAt,
			CreatedAt:      stale.Add(-24 * time.Hour),
			UpdatedAt:      stale,
		}
		require.NoError(t, ts.Create(ctx, tk))
		// Create never sets resolved_at/closed_at; write them the way a status
		// change would, directly, to seed the pre-#191 invariant violation —
		// via seedTicketState (see its doc comment), not ts.Update, so
		// updated_at lands at the controlled "stale" value these tests assert
		// against, not time.Now().
		seedTicketState(t, ctx, tx, tk)
		return tk
	}

	// Bad row #1: moved off Resolved, resolved_at never cleared.
	badOpen := mk("Mig bad open", newSt.ID, &stale, nil)
	// Bad row #2: sitting in Closed with no closed_at, and no history to
	// recover a transition instant from — must fall back to updated_at.
	badClosed := mk("Mig bad closed", closedSt.ID, nil, nil)
	// Bad row #3: sitting in Resolved with a stale closed_at left over from
	// resolving a previously-Closed ticket before that bug was fixed.
	badResolvedStaleClosed := mk("Mig bad resolved stale closed", resolvedSt.ID, &stale, &stale)
	// Control: correctly Resolved, must keep its timestamp.
	goodResolved := mk("Mig good resolved", resolvedSt.ID, &stale, nil)
	// Control: an ordinary open ticket with nothing set, must stay untouched.
	goodOpen := mk("Mig good open", newSt.ID, nil, nil)
	// Control: the ordinary, correct Closed shape — both resolved_at and
	// closed_at set (applyStatusTimestamps's closedID case never clears
	// ResolvedAt). Must survive untouched; see #208.
	goodClosed := mk("Mig good closed", closedSt.ID, &stale, &stale)

	// #237: any non-terminal status carrying a stale resolved_at must have it
	// cleared, not just the system 'New' status the old, narrower R1
	// accidentally singled out.
	badInProgress := mk("Mig bad in progress", inProgressSt.ID, &stale, nil)
	badCustom := mk("Mig bad custom", customStatusID, &stale, nil)
	badPending := mk("Mig bad pending", pendingSt.ID, &stale, nil)

	// #239: a reopened ticket's stale closed_at must be cleared too, not just
	// a stale resolved_at.
	badOpenStaleClosed := mk("Mig bad open stale closed", newSt.ID, nil, &stale)
	badInProgressStaleBoth := mk("Mig bad in progress stale both", inProgressSt.ID, &stale, &stale)

	// #238/R2: sitting in Resolved with resolved_at itself NULL (the pre-#102
	// UpdateStatus shape) — recovered from history when it exists, and from
	// updated_at when it does not.
	badResolvedWithHistory := mk("Mig bad resolved no resolved_at with history", resolvedSt.ID, nil, nil)
	seedHistory(t, ctx, ts, badResolvedWithHistory.ID, nil, newSt.ID, badResolvedWithHistory.CreatedAt)
	seedHistory(t, ctx, ts, badResolvedWithHistory.ID, &newSt.ID, resolvedSt.ID, stale.Add(-2*time.Hour))
	// Duplicate Resolved->Resolved row, as pre-#102 Resolve appended on a
	// re-resolve — must not be picked over the real stint start.
	seedHistory(t, ctx, ts, badResolvedWithHistory.ID, &resolvedSt.ID, resolvedSt.ID, stale.Add(-1*time.Hour))

	badResolvedNoHistory := mk("Mig bad resolved no resolved_at no history", resolvedSt.ID, nil, nil)

	// #241/R4: sitting in Closed with closed_at NULL, recovered from history.
	badClosedWithHistory := mk("Mig bad closed with history", closedSt.ID, nil, nil)
	seedHistory(t, ctx, ts, badClosedWithHistory.ID, &newSt.ID, closedSt.ID, stale.Add(-3*time.Hour))

	// Inconsistent history: the ticket is now in Closed, but the last history
	// row says it LEFT Closed, with no row recording it coming back — a door
	// that moved it without writing history. The recovery rule must fall back
	// to updated_at rather than resurrect the earlier, wrong stint.
	badClosedInconsistentHistory := mk("Mig bad closed inconsistent history", closedSt.ID, nil, nil)
	seedHistory(t, ctx, ts, badClosedInconsistentHistory.ID, &newSt.ID, closedSt.ID, stale.Add(-5*time.Hour))
	seedHistory(t, ctx, ts, badClosedInconsistentHistory.ID, &closedSt.ID, inProgressSt.ID, stale.Add(-4*time.Hour))

	// Duplicate Closed->Closed row, as pre-#102 Close appended on a re-close —
	// must not be picked over the real stint start.
	badClosedDupRows := mk("Mig bad closed dup rows", closedSt.ID, nil, nil)
	seedHistory(t, ctx, ts, badClosedDupRows.ID, &newSt.ID, closedSt.ID, stale.Add(-3*time.Hour))
	seedHistory(t, ctx, ts, badClosedDupRows.ID, &closedSt.ID, closedSt.ID, stale.Add(-1*time.Hour))

	// Run the migration's own SQL, verbatim, statement by statement (comments
	// stripped, dollar-quote aware, so the #230 DO $$ ... $$ guard block's
	// internal semicolons are not mistaken for statement boundaries).
	execMigrationFile(t, ctx, tx, "migrations/000028_repair_resolved_status_invariant.up.sql", nil)

	get := func(id uuid.UUID) ticket.Ticket {
		tk, err := ts.GetByID(ctx, id)
		require.NoError(t, err)
		return tk
	}

	repairedOpen := get(badOpen.ID)
	require.Nil(t, repairedOpen.ResolvedAt, "a ticket not in Resolved must have resolved_at cleared")

	repairedClosed := get(badClosed.ID)
	require.NotNil(t, repairedClosed.ClosedAt, "a Closed ticket must be stamped with closed_at")
	require.True(t, repairedClosed.ClosedAt.Equal(stale),
		"with no history to recover from, closed_at must fall back to updated_at, not now()")

	stillResolved := get(goodResolved.ID)
	require.NotNil(t, stillResolved.ResolvedAt, "a correctly-Resolved ticket must keep its timestamp")

	stillOpen := get(goodOpen.ID)
	require.Nil(t, stillOpen.ResolvedAt)
	require.Nil(t, stillOpen.ClosedAt)

	repairedResolved := get(badResolvedStaleClosed.ID)
	require.NotNil(t, repairedResolved.ResolvedAt, "a Resolved ticket must keep its resolved_at")
	require.Nil(t, repairedResolved.ClosedAt, "a Resolved ticket must have a stale closed_at cleared")

	stillClosed := get(goodClosed.ID)
	require.NotNil(t, stillClosed.ResolvedAt, "a Closed ticket must NOT have resolved_at cleared (#208)")
	require.NotNil(t, stillClosed.ClosedAt, "a Closed ticket must keep its closed_at")
	require.True(t, stillClosed.ClosedAt.Equal(stale), "a correctly-Closed ticket's closed_at must not move")

	require.Nil(t, get(badInProgress.ID).ResolvedAt, "#237: a stale resolved_at on an In Progress ticket must be cleared")
	require.Nil(t, get(badCustom.ID).ResolvedAt, "#237: a stale resolved_at on a custom-status ticket must be cleared")
	require.Nil(t, get(badPending.ID).ResolvedAt, "#237: a stale resolved_at on a Pending ticket must be cleared")

	require.Nil(t, get(badOpenStaleClosed.ID).ClosedAt, "#239: a stale closed_at on a reopened (New) ticket must be cleared")
	reopenedBoth := get(badInProgressStaleBoth.ID)
	require.Nil(t, reopenedBoth.ResolvedAt, "#237: In Progress with stale resolved_at AND closed_at: resolved_at cleared")
	require.Nil(t, reopenedBoth.ClosedAt, "#239: In Progress with stale resolved_at AND closed_at: closed_at cleared")

	repairedResolvedHistory := get(badResolvedWithHistory.ID)
	require.NotNil(t, repairedResolvedHistory.ResolvedAt, "#238: a Resolved ticket with no resolved_at must have one recovered")
	require.True(t, repairedResolvedHistory.ResolvedAt.Equal(stale.Add(-2*time.Hour)),
		"the recovered resolved_at must be the start of the CURRENT Resolved stint, not the duplicate re-resolve row, "+
			"and history must win over updated_at")

	repairedResolvedNoHistory := get(badResolvedNoHistory.ID)
	require.NotNil(t, repairedResolvedNoHistory.ResolvedAt)
	require.True(t, repairedResolvedNoHistory.ResolvedAt.Equal(stale),
		"with no history, a Resolved ticket's resolved_at must fall back to updated_at")

	repairedClosedHistory := get(badClosedWithHistory.ID)
	require.NotNil(t, repairedClosedHistory.ClosedAt)
	require.True(t, repairedClosedHistory.ClosedAt.Equal(stale.Add(-3*time.Hour)),
		"#241: the recovered closed_at must be the start of the CURRENT Closed stint")

	repairedClosedInconsistent := get(badClosedInconsistentHistory.ID)
	require.NotNil(t, repairedClosedInconsistent.ClosedAt)
	require.True(t, repairedClosedInconsistent.ClosedAt.Equal(stale),
		"when history says the ticket LEFT Closed with nothing recording it coming back, the recovery rule must fall "+
			"back to updated_at rather than resurrect the earlier, wrong stint")

	repairedClosedDup := get(badClosedDupRows.ID)
	require.NotNil(t, repairedClosedDup.ClosedAt)
	require.True(t, repairedClosedDup.ClosedAt.Equal(stale.Add(-3*time.Hour)),
		"a duplicate Closed->Closed history row must not be picked over the real stint start")
}

// TestMigration_AbortsWhenSystemStatusRenamed pins #230: migration 000028's
// destructive first statement matches by status NAME ('Resolved', 'Closed'),
// which the admin API cannot fully guard against — it blocks DEACTIVATING a
// system status but not RENAMING one (handleUpdateStatus / SaveStatus has no
// such check). If "Closed" were renamed on a running instance before this
// migration next ran on an upgrade, the name-based exclusion would silently
// stop matching every genuinely-Closed ticket, reproducing #208's original
// data-loss bug through a different path — irreversibly, since the down
// migration is a no-op.
//
// This renames the Closed status directly at the SQL level (the admin API's
// own guard is out of scope for a migration test — the defense this pins is
// the migration's own, independent of whatever the API layer enforces) and
// confirms the migration now fails loudly instead of silently wiping data.
func TestMigration_AbortsWhenSystemStatusRenamed(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()

	sql, err := os.ReadFile("migrations/000028_repair_resolved_status_invariant.up.sql")
	require.NoError(t, err)

	tx, err := db.SQL.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	ctx := context.Background()

	_, err = tx.ExecContext(ctx, `UPDATE statuses SET name = 'Finished' WHERE name = 'Closed'`)
	require.NoError(t, err, "seeding the rename this migration must guard against")

	var stripped strings.Builder
	for _, line := range strings.Split(string(sql), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		stripped.WriteString(line)
		stripped.WriteByte('\n')
	}

	var execErr error
	for _, stmt := range splitSQLStatements(stripped.String()) {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, execErr = tx.ExecContext(ctx, stmt); execErr != nil {
			break
		}
	}

	require.Error(t, execErr,
		"the migration must fail loudly when a system status has been renamed, not silently misclassify rows by name (#230)")
	require.Contains(t, execErr.Error(), "Closed",
		"the failure should name which system status it could not find, to make the cause obvious")
}

// TestMigration_AbortsWhenSystemStatusRenamedAndNameReused pins #232: the
// guard TestMigration_AbortsWhenSystemStatusRenamed exercises counted rows by
// bare NAME only, not kind = 'system' — the column migration 000001 seeded
// specifically for this. Since the status name column is UNIQUE, renaming
// the real system-kind "Closed" status frees that name for a brand new
// CUSTOM-kind status to reuse. Before #232, count(*) = 1 for name = 'Closed'
// still passed in that exact shape (there is, once again, exactly one row
// named "Closed" — it is just the wrong one), so the guard did not catch it,
// and the destructive statements below would have gone on to operate on the
// new custom status instead of the real system one — #208's data loss,
// through yet another path. This seeds exactly that shape (rename, then
// recreate with the freed name as a CUSTOM status) and confirms the
// migration now aborts loudly instead of silently misclassifying rows.
func TestMigration_AbortsWhenSystemStatusRenamedAndNameReused(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()

	sql, err := os.ReadFile("migrations/000028_repair_resolved_status_invariant.up.sql")
	require.NoError(t, err)

	tx, err := db.SQL.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	ctx := context.Background()

	_, err = tx.ExecContext(ctx, `UPDATE statuses SET name = 'Finished' WHERE name = 'Closed'`)
	require.NoError(t, err, "seeding the rename this migration must guard against")
	_, err = tx.ExecContext(ctx,
		`INSERT INTO statuses (name, kind, sort_order, color) VALUES ('Closed', 'custom', 999, '#000000')`)
	require.NoError(t, err, "seeding a NEW custom status reusing the freed 'Closed' name")

	var stripped strings.Builder
	for _, line := range strings.Split(string(sql), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		stripped.WriteString(line)
		stripped.WriteByte('\n')
	}

	var execErr error
	for _, stmt := range splitSQLStatements(stripped.String()) {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, execErr = tx.ExecContext(ctx, stmt); execErr != nil {
			break
		}
	}

	require.Error(t, execErr,
		"the migration must fail loudly when a system status has been renamed and its name reused by a new "+
			"custom status, not silently operate on the wrong row (#232)")
	require.Contains(t, execErr.Error(), "Closed",
		"the failure should name which system status it could not find, to make the cause obvious")
	require.Contains(t, execErr.Error(), "SYSTEM",
		"the failure should make clear this is about the SYSTEM status specifically, not merely the name")
}

// TestMigration_028LocksStatusesBeforeGuard is a structural pin for #248: the
// LOCK TABLE statement must be the very first statement in the file, strictly
// before the guard's own SELECTs — a lock taken any later would leave a
// window between the guard's snapshot and the lock being granted, in which a
// still-running old app instance could commit a rename the guard never saw.
// This is a structural check rather than a behavioural ordering test: proving
// the ordering behaviourally would mean committing a rename of a system
// status into the shared test database (see TestMigration_028HoldsStatuses…
// below for why that is out of bounds here — it would race every other
// package's tests running against the same database at the same time).
func TestMigration_028LocksStatusesBeforeGuard(t *testing.T) {
	sqlBytes, err := os.ReadFile("migrations/000028_repair_resolved_status_invariant.up.sql")
	require.NoError(t, err)

	var stripped strings.Builder
	for _, line := range strings.Split(string(sqlBytes), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		stripped.WriteString(line)
		stripped.WriteByte('\n')
	}

	stmts := splitSQLStatements(stripped.String())
	require.NotEmpty(t, stmts, "the migration file must contain at least one statement")
	require.Equal(t, "LOCK TABLE statuses IN SHARE MODE", strings.TrimSpace(stmts[0]),
		"#248: LOCK TABLE must be the first statement in the file, before the guard's DO block")
}

// TestMigration_028HoldsStatusesLockUntilCommit pins #248's actual locking
// behaviour against real Postgres: migration 000028's SHARE lock on statuses
// must be held for the rest of its transaction, so a concurrent rename of a
// system status cannot commit — and therefore cannot land between the
// guard's snapshot and this migration's own destructive statements — until
// this transaction itself commits or rolls back.
//
// This runs 027+028 in one transaction and leaves it OPEN (never committed,
// only rolled back at the end — nothing this test does is ever persisted),
// then attempts a rename of the system Closed status from a second,
// independent connection with a short lock_timeout. The rename's implicit
// ROW EXCLUSIVE lock on statuses conflicts with the first transaction's SHARE
// lock, so it must block and then time out, rather than succeed.
func TestMigration_028HoldsStatusesLockUntilCommit(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()

	tx1, err := db.SQL.Begin()
	require.NoError(t, err)
	defer func() { _ = tx1.Rollback() }()
	runMigration027And028(t, ctx, tx1)

	// A second, independent connection from the pool — tx1's own connection
	// is busy holding its transaction open.
	tx2, err := db.SQL.Begin()
	require.NoError(t, err)
	defer func() { _ = tx2.Rollback() }()

	_, err = tx2.ExecContext(ctx, `SET LOCAL lock_timeout = '200ms'`)
	require.NoError(t, err)

	_, err = tx2.ExecContext(ctx, `UPDATE statuses SET name = name WHERE kind = 'system' AND name = 'Closed'`)
	require.Error(t, err,
		"#248: tx1's SHARE lock on statuses, held until it commits or rolls back, must block this UPDATE's implicit ROW EXCLUSIVE lock until lock_timeout fires")

	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr), "expected a Postgres error, got: %v", err)
	require.Equal(t, "55P03", pgErr.Code,
		"expected lock_not_available (the lock_timeout firing while waiting on tx1's lock), got: %v", err)

	// Neither transaction is committed: tx1's LOCK and repairs, and tx2's
	// (never applied, since it errored) rename attempt, are both discarded by
	// the deferred rollbacks above.
}
