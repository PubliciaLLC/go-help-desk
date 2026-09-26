package database_test

import (
	"context"
	"database/sql"
	"os"
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

// TestMigration_FrozenElapsedBackfillClipsPendingTime pins #222: migration
// 000027's backfill for response_elapsed_at_met_seconds ignored pending_since
// for a ticket that was Pending at upgrade time, over-counting elapsed time by
// (first_response_at - pending_since) whenever the response landed after the
// ticket went Pending. This runs only the migration's own UPDATE statements
// (read from disk, not reimplemented) — the ALTER TABLE half already ran when
// the test database's schema was set up — against a row seeded to exactly
// reproduce that shape, and checks the backfilled value matches what
// sla.Elapsed computes live for the same instant.
func TestMigration_FrozenElapsedBackfillClipsPendingTime(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()

	tx, err := db.SQL.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	q := dbgen.New(tx)

	us := userstore.New(q)
	cs := categorystore.New(q)
	ts := ticketstore.New(q)
	sls := slastore.New(q)

	reporter := user.User{
		ID: uuid.New(), Email: "backfill-" + uuid.NewString() + "@test.local",
		DisplayName: "Reporter", Role: user.RoleUser,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, reporter))
	cat := category.Category{ID: uuid.New(), Name: "Backfill " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))
	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)

	// created 4 hours ago; entered Pending 2 hours ago; responded 1 hour ago
	// (i.e. 1 hour AFTER going Pending, with the interval still open — the
	// ticket is Pending right now, exactly the "Pending at upgrade time" shape
	// migration 000025 populates pending_since for).
	now := time.Now().UTC().Truncate(time.Millisecond)
	created := now.Add(-4 * time.Hour)
	pendingSince := now.Add(-2 * time.Hour)
	firstResponseAt := now.Add(-1 * time.Hour)
	const priorPausedSeconds = int64(600) // 10 minutes accumulated from an earlier, already-closed pause

	tk := ticket.Ticket{
		ID:               uuid.New(),
		TrackingNumber:   ticket.TrackingNumber("BF-" + uuid.NewString()[:8]),
		Subject:          "Backfill me",
		Description:      "Backfill me",
		CategoryID:       cat.ID,
		Priority:         ticket.PriorityMedium,
		StatusID:         newSt.ID,
		ReporterUserID:   &reporter.ID,
		CreatedAt:        created,
		UpdatedAt:        now,
		PendingSince:     &pendingSince,
		SLAPausedSeconds: priorPausedSeconds,
	}
	require.NoError(t, ts.Create(ctx, tk))
	require.NoError(t, ts.Update(ctx, tk)) // Create doesn't set pending_since/sla_paused_seconds; write them directly

	slaPolicy := sla.Policy{ID: uuid.New(), Name: "Backfill policy", ResponseTargetMin: 30, ResolutionTargetMin: 480}
	require.NoError(t, sls.CreatePolicy(ctx, slaPolicy))
	require.NoError(t, sls.CreateRecord(ctx, sla.Record{
		TicketID:        tk.ID,
		PolicyID:        slaPolicy.ID,
		FirstResponseAt: &firstResponseAt, // legacy shape: timestamp set, frozen-seconds column still NULL
	}))

	// Run ONLY migration 000027's UPDATE statements (skip its ALTER TABLE,
	// which already applied when this test database's schema was built) —
	// read from disk, comments stripped, verbatim otherwise.
	sqlBytes, err := os.ReadFile("migrations/000027_sla_frozen_elapsed.up.sql")
	require.NoError(t, err)
	var stripped strings.Builder
	for _, line := range strings.Split(string(sqlBytes), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		stripped.WriteString(line)
		stripped.WriteByte('\n')
	}
	for _, stmt := range splitSQLStatements(stripped.String()) {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" || strings.HasPrefix(strings.ToUpper(stmt), "ALTER TABLE") {
			continue
		}
		_, err := tx.ExecContext(ctx, stmt)
		require.NoError(t, err, "executing migration statement: %s", stmt)
	}

	rec, err := sls.GetRecord(ctx, tk.ID)
	require.NoError(t, err)
	require.NotNil(t, rec.ResponseElapsedAtMetSeconds)

	// What sla.Elapsed computes live for the same ticket shape and instant —
	// the backfilled value must match this exactly, not the old formula's
	// over-count.
	want := int64(sla.Elapsed(tk, firstResponseAt) / time.Second)
	require.Equal(t, want, *rec.ResponseElapsedAtMetSeconds,
		"the backfilled value must equal sla.Elapsed's own pending-aware computation, not over-count by the open Pending interval")

	// Sanity: the buggy formula (no pending clip) would have produced
	// firstResponseAt - created - priorPausedSeconds, i.e. 3h - 10m — provably
	// larger than the correct answer, so this test would have caught the
	// regression it pins.
	buggy := int64((firstResponseAt.Sub(created)).Seconds()) - priorPausedSeconds
	require.Less(t, want, buggy, "sanity check: the pending-aware answer really is smaller than the naive one")
}

// execMigrationFile runs one migration file's statements (read from disk,
// comments stripped, dollar-quote aware) against tx, in order, skipping any
// statement for which skip returns true. Used to replay a migration's data
// repair/backfill statements against rows seeded directly to reproduce a
// legacy shape, without reimplementing the SQL in Go (CLAUDE.md: "Do not mock
// the DB").
func execMigrationFile(t *testing.T, ctx context.Context, tx *sql.Tx, path string, skip func(stmt string) bool) {
	t.Helper()
	sqlBytes, err := os.ReadFile(path)
	require.NoError(t, err)
	var stripped strings.Builder
	for _, line := range strings.Split(string(sqlBytes), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		stripped.WriteString(line)
		stripped.WriteByte('\n')
	}
	for _, stmt := range splitSQLStatements(stripped.String()) {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" || (skip != nil && skip(stmt)) {
			continue
		}
		_, err := tx.ExecContext(ctx, stmt)
		require.NoError(t, err, "executing migration statement: %s", stmt)
	}
}

// TestMigration_BackfillsPreExistingSLAInvariantViolations pins #226: SLA
// tracking already existed before this development cycle, so a real deployed
// instance can already have sla_records rows written under the OLD rules —
// #219 ("a resolution is a response") and #220 ("closed without resolving
// still gets a resolution recorded") change the CODE's behavior going
// forward, but do nothing for rows that already violate them. This runs
// migration 000027's SQL (read from disk, verbatim, skipping only the ALTER
// TABLE — this test database's schema already has the two columns) followed
// by migration 000028's SQL (read from disk, verbatim) — #231 moved the
// #226(a)/(b) backfill from the former into the latter, so both must run in
// their real order for this test to exercise the actual repair path — against
// two seeded legacy shapes and confirms both are repaired, and that the
// resulting SLA status reads as a correct, frozen, non-growing indicator
// afterward rather than a permanent false breach.
func TestMigration_BackfillsPreExistingSLAInvariantViolations(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()

	tx, err := db.SQL.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	q := dbgen.New(tx)

	us := userstore.New(q)
	cs := categorystore.New(q)
	ts := ticketstore.New(q)
	sls := slastore.New(q)

	reporter := user.User{
		ID: uuid.New(), Email: "backfill-invariant-" + uuid.NewString() + "@test.local",
		DisplayName: "Reporter", Role: user.RoleUser,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, reporter))
	cat := category.Category{ID: uuid.New(), Name: "Backfill invariant " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))
	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)
	closedSt, err := ts.GetStatusByName(ctx, ticket.StatusNameClosed)
	require.NoError(t, err)

	policy := sla.Policy{ID: uuid.New(), Name: "Backfill invariant policy", ResponseTargetMin: 30, ResolutionTargetMin: 480}
	require.NoError(t, sls.CreatePolicy(ctx, policy))

	now := time.Now().UTC().Truncate(time.Millisecond)

	// Shape (a): resolved without ever getting a prior staff reply — allowed
	// under the OLD rules, before #219. resolved_at is set; first_response_at
	// is NULL.
	resolvedAtA := now.Add(-10 * time.Hour)
	tkA := ticket.Ticket{
		ID: uuid.New(), TrackingNumber: ticket.TrackingNumber("BFA-" + uuid.NewString()[:8]),
		Subject: "shape a", Description: "shape a", CategoryID: cat.ID, Priority: ticket.PriorityMedium,
		StatusID: newSt.ID, ReporterUserID: &reporter.ID, ResolvedAt: &resolvedAtA,
		CreatedAt: now.Add(-24 * time.Hour), UpdatedAt: resolvedAtA,
	}
	// Actually stays New — this fixture is a #244-shaped ticket (resolved_at
	// set, but the ticket is not sitting in Resolved) whose sla_records row
	// already carries a resolution fact (set below), which is exactly what
	// C1 skips (r.resolved_at IS NULL is false for it). Kept as New rather
	// than "corrected" to Resolved, since re-shaping it would stop pinning
	// what this test actually exercises.
	tkA.StatusID = newSt.ID
	require.NoError(t, ts.Create(ctx, tkA))
	require.NoError(t, ts.Update(ctx, tkA)) // Create never sets resolved_at; write it directly, as a real resolve would have.
	require.NoError(t, sls.CreateRecord(ctx, sla.Record{TicketID: tkA.ID, PolicyID: policy.ID, ResolvedAt: &resolvedAtA}))

	// Shape (b): closed directly from an open status, never separately
	// resolved — allowed under the OLD rules, before #220. Both resolved_at
	// (ticket AND sla_records) are NULL; closed_at is set.
	closedAtB := now.Add(-5 * time.Hour)
	tkB := ticket.Ticket{
		ID: uuid.New(), TrackingNumber: ticket.TrackingNumber("BFB-" + uuid.NewString()[:8]),
		Subject: "shape b", Description: "shape b", CategoryID: cat.ID, Priority: ticket.PriorityMedium,
		StatusID: closedSt.ID, ReporterUserID: &reporter.ID, ClosedAt: &closedAtB,
		CreatedAt: now.Add(-24 * time.Hour), UpdatedAt: closedAtB,
	}
	require.NoError(t, ts.Create(ctx, tkB))
	require.NoError(t, ts.Update(ctx, tkB)) // Create never sets closed_at; write it directly.
	require.NoError(t, sls.CreateRecord(ctx, sla.Record{TicketID: tkB.ID, PolicyID: policy.ID}))

	// Run migration 000027's SQL, verbatim, skipping only the ALTER TABLE
	// (this test database's schema already carries the two columns) — then
	// migration 000028's SQL, verbatim, in the SAME real order the two files
	// run in on a real upgrade. #231 moved the #226(a)/(b) backfill from the
	// former into the latter; running only 000027 (as this test used to)
	// would silently stop testing that backfill at all.
	skipAlterTable := func(stmt string) bool { return strings.HasPrefix(strings.ToUpper(stmt), "ALTER TABLE") }
	execMigrationFile(t, ctx, tx, "migrations/000027_sla_frozen_elapsed.up.sql", skipAlterTable)
	execMigrationFile(t, ctx, tx, "migrations/000028_repair_resolved_status_invariant.up.sql", nil)

	// Shape (a) repaired: first_response_at backfilled from resolved_at, and
	// its own frozen elapsed reading computed.
	recA, err := sls.GetRecord(ctx, tkA.ID)
	require.NoError(t, err)
	require.NotNil(t, recA.ResolvedAt)
	require.True(t, recA.ResolvedAt.Equal(resolvedAtA), "shape (a)'s own resolved_at must not move")
	require.NotNil(t, recA.FirstResponseAt, "#226(a): first_response_at must be backfilled from resolved_at")
	require.True(t, recA.FirstResponseAt.Equal(resolvedAtA))
	require.NotNil(t, recA.ResponseElapsedAtMetSeconds)

	// Shape (b) repaired: resolved_at backfilled from the ticket's closed_at
	// (it had no resolved_at of its own), and — since this also leaves it
	// with no first_response_at — #226(a) then backfills that too from the
	// newly-set resolved_at, in the same migration run.
	recB, err := sls.GetRecord(ctx, tkB.ID)
	require.NoError(t, err)
	require.NotNil(t, recB.ResolvedAt, "#226(b): resolved_at must be backfilled from the ticket's closed_at")
	require.True(t, recB.ResolvedAt.Equal(closedAtB))
	require.NotNil(t, recB.ResolutionElapsedAtMetSeconds)
	require.NotNil(t, recB.FirstResponseAt, "#226(a) cascading from (b): a ticket repaired to have a resolution also gets the response backfill")
	require.True(t, recB.FirstResponseAt.Equal(closedAtB))
	require.NotNil(t, recB.ResponseElapsedAtMetSeconds)

	// The indicator itself, checked much later: frozen (a MetAt to freeze
	// against), not permanently red, not falsely breached — never a live,
	// ever-growing Elapsed(t, now) against a ticket that will never move
	// again.
	muchLater := now.Add(24 * 30 * time.Hour)
	tkAAfter, err := ts.GetByID(ctx, tkA.ID)
	require.NoError(t, err)
	statusA := sla.StatusFor(recA, policy, tkAAfter, muchLater)
	require.NotNil(t, statusA.Response.MetAt, "shape (a) must read as a MET response target, not an outstanding one")
	require.False(t, sla.IsResponseBreached(recA, policy, tkAAfter, muchLater),
		"an on-time resolution must never read as a false response breach (#219)")

	tkBAfter, err := ts.GetByID(ctx, tkB.ID)
	require.NoError(t, err)
	statusB := sla.StatusFor(recB, policy, tkBAfter, muchLater)
	require.NotNil(t, statusB.Resolution.MetAt, "shape (b) must read as a MET resolution, not an outstanding one")
	require.NotNil(t, statusB.Response.MetAt)
}

// TestMigration_SLABackfillReachesClosedAtStillNullRows pins #231's own
// reproduction directly: a ticket sitting in Closed whose closed_at was NEVER
// stamped (the exact legacy shape migration 000028 exists to repair) and
// whose sla_records.resolved_at is NULL. Before #231, migration 000027's
// backfill ran BEFORE 000028's repair and keyed on `closed_at IS NOT NULL` —
// at that point in the sequence closed_at was still NULL for this row, so the
// backfill skipped it, and nothing ever revisited it afterward. This runs the
// two files in their real order and confirms the backfill now reaches the
// row — checking, in between, that 000027 ALONE still does not (proving this
// test would have caught the original bug, not just exercised the fixed
// path).
func TestMigration_SLABackfillReachesClosedAtStillNullRows(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()

	tx, err := db.SQL.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	q := dbgen.New(tx)

	us := userstore.New(q)
	cs := categorystore.New(q)
	ts := ticketstore.New(q)
	sls := slastore.New(q)

	reporter := user.User{
		ID: uuid.New(), Email: "backfill-231-" + uuid.NewString() + "@test.local",
		DisplayName: "Reporter", Role: user.RoleUser,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, reporter))
	cat := category.Category{ID: uuid.New(), Name: "Backfill 231 " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))
	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)
	closedSt, err := ts.GetStatusByName(ctx, ticket.StatusNameClosed)
	require.NoError(t, err)

	policy := sla.Policy{ID: uuid.New(), Name: "Backfill 231 policy", ResponseTargetMin: 30, ResolutionTargetMin: 480}
	require.NoError(t, sls.CreatePolicy(ctx, policy))

	now := time.Now().UTC().Truncate(time.Millisecond)

	// The exact legacy shape #231 describes: sitting in Closed, closed_at
	// NEVER stamped, sla_records.resolved_at NULL.
	tk := ticket.Ticket{
		ID: uuid.New(), TrackingNumber: ticket.TrackingNumber("BF231-" + uuid.NewString()[:8]),
		Subject: "closed with no closed_at", Description: "closed with no closed_at",
		CategoryID: cat.ID, Priority: ticket.PriorityMedium,
		StatusID: newSt.ID, ReporterUserID: &reporter.ID,
		CreatedAt: now.Add(-24 * time.Hour), UpdatedAt: now,
	}
	require.NoError(t, ts.Create(ctx, tk))
	tk.StatusID = closedSt.ID // Closed, but ClosedAt left nil — Update never touches it here.
	require.NoError(t, ts.Update(ctx, tk))
	require.NoError(t, sls.CreateRecord(ctx, sla.Record{TicketID: tk.ID, PolicyID: policy.ID}))

	stored, err := ts.GetByID(ctx, tk.ID)
	require.NoError(t, err)
	require.Nil(t, stored.ClosedAt, "sanity check: the seeded legacy shape has no closed_at yet")

	// Run 000027 ALONE first (skipping only its ALTER TABLE) and confirm the
	// bug's own symptom: the backfill does NOT reach this row yet, because
	// closed_at is still NULL at this point in the sequence.
	skipAlterTable := func(stmt string) bool { return strings.HasPrefix(strings.ToUpper(stmt), "ALTER TABLE") }
	execMigrationFile(t, ctx, tx, "migrations/000027_sla_frozen_elapsed.up.sql", skipAlterTable)

	midway, err := sls.GetRecord(ctx, tk.ID)
	require.NoError(t, err)
	require.Nil(t, midway.ResolvedAt,
		"sanity check: migration 000027 alone must not backfill this row (closed_at is still NULL at this point) — "+
			"otherwise this test would not be exercising #231's actual bug")

	// Now run 000028, which stamps closed_at AND (post-#231) runs the moved
	// SLA backfill afterward, in the same file.
	execMigrationFile(t, ctx, tx, "migrations/000028_repair_resolved_status_invariant.up.sql", nil)

	repaired, err := ts.GetByID(ctx, tk.ID)
	require.NoError(t, err)
	require.NotNil(t, repaired.ClosedAt, "000028 must stamp closed_at for a Closed ticket that never had one")

	rec, err := sls.GetRecord(ctx, tk.ID)
	require.NoError(t, err)
	require.NotNil(t, rec.ResolvedAt,
		"#231: the SLA backfill must now reach this row once closed_at exists, in the same migration run")
	require.True(t, rec.ResolvedAt.Equal(*repaired.ClosedAt))
	// #246: this fixture's resolution instant is estimated (no fact backs
	// it — see the #243 regression pin below), so S1 deliberately leaves
	// the frozen elapsed number NULL rather than filling it with a
	// number derived from an estimate: that NULL is the durable marker a
	// second migration run reads to know this row must never be stamped.
	require.Nil(t, rec.ResolutionElapsedAtMetSeconds)
	require.NotNil(t, rec.FirstResponseAt, "#226(a) cascades from the newly-backfilled resolved_at")

	// #243 regression pin: this fixture has no history and no fact for its
	// resolution — C1 finds nothing, so C2 estimates the instant from
	// updated_at/closed_at. Its resolution reads over a wide 480-minute
	// target from a ticket created 24h ago, so before the estimated gate
	// existed the old SQL stamped both breach columns here.
	require.Nil(t, rec.ResolutionBreachedAt,
		"#243: an estimated resolution instant (no fact backs it) must never get a breach stamp")
	require.Nil(t, rec.ResponseBreachedAt, "#243: same, for the #226(a) response cascade")
}

// TestMigration_SLABackfillStampsBreachForLateLegacyTicket pins #235: a
// legacy ticket closed-without-resolving whose actual close instant was PAST
// the policy's resolution target must have resolution_breached_at (and, via
// the #226(a) response cascade, response_breached_at) stamped by the
// backfill — the same threshold SetSLAResolved/SetSLAFirstResponse apply at
// record time (#217/#228). Before this fix, the backfill filled the
// timestamp and frozen-elapsed columns but never the breach columns, even
// though its own comment claimed equivalence with what RecordResolved would
// have written.
//
// #241 (round 4 review): this fixture used to give tkOnTime no history and
// rely on ts.Update's ClosedAt: nil leaving tickets.closed_at to be stamped
// with now() by the pre-#241 migration — which is exactly the bug #241
// fixed, so a ticket "closed" 10 minutes after creation only read as on-time
// because the migration back then dated the close at the moment IT ran, not
// at the moment the ticket actually closed. Now every ticket here is the
// same age (created 4h ago) and the ONLY thing that decides late vs on-time
// is the recovered (or, for tkFallback, the updated_at-derived) close
// instant, proving the migration measures the historical close, not the
// upgrade time.
func TestMigration_SLABackfillStampsBreachForLateLegacyTicket(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()

	tx, err := db.SQL.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	q := dbgen.New(tx)

	us := userstore.New(q)
	cs := categorystore.New(q)
	ts := ticketstore.New(q)
	sls := slastore.New(q)

	reporter := user.User{
		ID: uuid.New(), Email: "backfill-235-" + uuid.NewString() + "@test.local",
		DisplayName: "Reporter", Role: user.RoleUser,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, reporter))
	cat := category.Category{ID: uuid.New(), Name: "Backfill 235 " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))
	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)
	closedSt, err := ts.GetStatusByName(ctx, ticket.StatusNameClosed)
	require.NoError(t, err)

	// A tight policy: 30 minutes for both targets.
	policy := sla.Policy{ID: uuid.New(), Name: "Backfill 235 policy", ResponseTargetMin: 30, ResolutionTargetMin: 30}
	require.NoError(t, sls.CreatePolicy(ctx, policy))

	now := time.Now().UTC().Truncate(time.Millisecond)
	created := now.Add(-4 * time.Hour)

	// Late: history says it closed 2 hours after creation — well past the
	// 30-minute target — closed_at never stamped (legacy shape), never
	// separately resolved. UpdatedAt is "now" (much later than the recovered
	// close), so this also proves history outranks updated_at.
	tkLate := ticket.Ticket{
		ID: uuid.New(), TrackingNumber: ticket.TrackingNumber("BF235L-" + uuid.NewString()[:8]),
		Subject: "late legacy close", Description: "late legacy close",
		CategoryID: cat.ID, Priority: ticket.PriorityMedium,
		StatusID: newSt.ID, ReporterUserID: &reporter.ID,
		CreatedAt: created, UpdatedAt: now,
	}
	require.NoError(t, ts.Create(ctx, tkLate))
	seedHistory(t, ctx, ts, tkLate.ID, nil, newSt.ID, created)
	tkLate.StatusID = closedSt.ID
	require.NoError(t, ts.Update(ctx, tkLate))
	seedHistory(t, ctx, ts, tkLate.ID, &newSt.ID, closedSt.ID, created.Add(2*time.Hour))
	require.NoError(t, sls.CreateRecord(ctx, sla.Record{TicketID: tkLate.ID, PolicyID: policy.ID}))

	// On-time: same age as tkLate (created 4h ago), but history says it
	// closed only 10 minutes after creation, under the 30-minute target.
	// Before #241, this ticket had no history and relied on the migration's
	// closed_at = now() to read as "on time" purely because it was seeded
	// close to the moment the test ran it — that is the exact bug #241
	// fixed, so this fixture must not rely on it any more.
	tkOnTime := ticket.Ticket{
		ID: uuid.New(), TrackingNumber: ticket.TrackingNumber("BF235O-" + uuid.NewString()[:8]),
		Subject: "on-time legacy close", Description: "on-time legacy close",
		CategoryID: cat.ID, Priority: ticket.PriorityMedium,
		StatusID: newSt.ID, ReporterUserID: &reporter.ID,
		CreatedAt: created, UpdatedAt: now,
	}
	require.NoError(t, ts.Create(ctx, tkOnTime))
	tkOnTime.StatusID = closedSt.ID
	require.NoError(t, ts.Update(ctx, tkOnTime))
	seedHistory(t, ctx, ts, tkOnTime.ID, &newSt.ID, closedSt.ID, created.Add(10*time.Minute))
	require.NoError(t, sls.CreateRecord(ctx, sla.Record{TicketID: tkOnTime.ID, PolicyID: policy.ID}))

	// Fallback: same age again, but no history at all — the recovery rule
	// has nothing to recover from and must fall back to updated_at, which
	// here is set to a specific instant (15 minutes after creation, on time)
	// rather than "now", so a wrong fallback to now() would read as late.
	tkFallback := ticket.Ticket{
		ID: uuid.New(), TrackingNumber: ticket.TrackingNumber("BF235F-" + uuid.NewString()[:8]),
		Subject: "legacy close no history", Description: "legacy close no history",
		CategoryID: cat.ID, Priority: ticket.PriorityMedium,
		StatusID: newSt.ID, ReporterUserID: &reporter.ID,
		CreatedAt: created, UpdatedAt: created.Add(15 * time.Minute),
	}
	require.NoError(t, ts.Create(ctx, tkFallback))
	tkFallback.StatusID = closedSt.ID
	// seedTicketState, not ts.Update: Update always stamps updated_at with
	// time.Now(), which would defeat this exact fallback pin (see its doc
	// comment).
	seedTicketState(t, ctx, tx, tkFallback)
	require.NoError(t, sls.CreateRecord(ctx, sla.Record{TicketID: tkFallback.ID, PolicyID: policy.ID}))

	skipAlterTable := func(stmt string) bool { return strings.HasPrefix(strings.ToUpper(stmt), "ALTER TABLE") }
	execMigrationFile(t, ctx, tx, "migrations/000027_sla_frozen_elapsed.up.sql", skipAlterTable)
	execMigrationFile(t, ctx, tx, "migrations/000028_repair_resolved_status_invariant.up.sql", nil)

	tkLateAfter, err := ts.GetByID(ctx, tkLate.ID)
	require.NoError(t, err)
	require.NotNil(t, tkLateAfter.ClosedAt)
	require.True(t, tkLateAfter.ClosedAt.Equal(created.Add(2*time.Hour)),
		"#241: closed_at must be recovered from history, not stamped with now()")

	recLate, err := sls.GetRecord(ctx, tkLate.ID)
	require.NoError(t, err)
	require.NotNil(t, recLate.ResolvedAt)
	require.True(t, recLate.ResolvedAt.Equal(created.Add(2*time.Hour)))
	require.NotNil(t, recLate.ResolutionBreachedAt,
		"#235: a legacy ticket that really was late must have resolution_breached_at stamped by the backfill")
	require.True(t, recLate.ResolutionBreachedAt.Equal(*recLate.ResolvedAt))
	require.NotNil(t, recLate.FirstResponseAt)
	require.True(t, recLate.FirstResponseAt.Equal(created.Add(2*time.Hour)))
	require.NotNil(t, recLate.ResponseBreachedAt,
		"#235: the #226(a) response cascade inherits the same late instant and must be stamped too")
	require.True(t, recLate.ResponseBreachedAt.Equal(created.Add(2*time.Hour)))

	tkOnTimeAfter, err := ts.GetByID(ctx, tkOnTime.ID)
	require.NoError(t, err)
	require.NotNil(t, tkOnTimeAfter.ClosedAt)
	require.True(t, tkOnTimeAfter.ClosedAt.Equal(created.Add(10*time.Minute)),
		"#241: against the pre-fix SQL, closed_at = now() gives this ticket a ~4h elapsed reading and this assertion "+
			"fails — the regression this fixture pins")

	recOnTime, err := sls.GetRecord(ctx, tkOnTime.ID)
	require.NoError(t, err)
	require.NotNil(t, recOnTime.ResolvedAt)
	require.True(t, recOnTime.ResolvedAt.Equal(created.Add(10*time.Minute)))
	require.Nil(t, recOnTime.ResolutionBreachedAt, "control: an on-time legacy close must not be stamped as breached")
	require.Nil(t, recOnTime.ResponseBreachedAt)

	tkFallbackAfter, err := ts.GetByID(ctx, tkFallback.ID)
	require.NoError(t, err)
	require.NotNil(t, tkFallbackAfter.ClosedAt)
	require.True(t, tkFallbackAfter.ClosedAt.Equal(created.Add(15*time.Minute)),
		"with no history, closed_at must fall back to updated_at")

	recFallback, err := sls.GetRecord(ctx, tkFallback.ID)
	require.NoError(t, err)
	require.Nil(t, recFallback.ResolutionBreachedAt)
	require.Nil(t, recFallback.ResponseBreachedAt)
}

// TestMigration_SLABackfillReachesResolvedTickets pins #238: before this fix,
// the SLA backfill's status key was 'Closed' alone (see #231's own note in
// migration 000028), so a ticket resolved under a pre-v1.2.0 build and still
// sitting in Resolved at upgrade was never reached — its sla_records row kept
// a permanently NULL resolved_at, and the very next breach sweep would stamp
// a false breach dated at the sweep, which nothing could ever undo. It also
// pins R2 (#237's companion fix, needed for #238 to be more than a partial
// fix): a Resolved ticket whose OWN resolved_at is NULL (the pre-#102
// UpdateStatus shape) needs that recovered before the SLA backfill's
// COALESCE(t.resolved_at, t.closed_at) has anything to read.
func TestMigration_SLABackfillReachesResolvedTickets(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()

	tx, err := db.SQL.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	q := dbgen.New(tx)

	us := userstore.New(q)
	cs := categorystore.New(q)
	ts := ticketstore.New(q)
	sls := slastore.New(q)

	reporter := user.User{
		ID: uuid.New(), Email: "backfill-238-" + uuid.NewString() + "@test.local",
		DisplayName: "Reporter", Role: user.RoleUser,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, reporter))
	cat := category.Category{ID: uuid.New(), Name: "Backfill 238 " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))
	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)
	resolvedSt, err := ts.GetStatusByName(ctx, ticket.StatusNameResolved)
	require.NoError(t, err)

	// A tight policy: 30 minutes for both targets.
	policy := sla.Policy{ID: uuid.New(), Name: "Backfill 238 policy", ResponseTargetMin: 30, ResolutionTargetMin: 30}
	require.NoError(t, sls.CreatePolicy(ctx, policy))

	now := time.Now().UTC().Truncate(time.Millisecond)
	created := now.Add(-4 * time.Hour)

	mkResolved := func(subject string, resolvedAt, closedAt *time.Time) ticket.Ticket {
		tk := ticket.Ticket{
			ID: uuid.New(), TrackingNumber: ticket.TrackingNumber("BF238-" + uuid.NewString()[:8]),
			Subject: subject, Description: subject,
			CategoryID: cat.ID, Priority: ticket.PriorityMedium,
			StatusID: resolvedSt.ID, ReporterUserID: &reporter.ID,
			ResolvedAt: resolvedAt, ClosedAt: closedAt,
			CreatedAt: created, UpdatedAt: now,
		}
		require.NoError(t, ts.Create(ctx, tk))
		require.NoError(t, ts.Update(ctx, tk)) // Create never sets resolved_at/closed_at; write them directly.
		require.NoError(t, sls.CreateRecord(ctx, sla.Record{TicketID: tk.ID, PolicyID: policy.ID}))
		return tk
	}

	resolvedLateAt := created.Add(2 * time.Hour)
	resLate := mkResolved("resolved late", &resolvedLateAt, nil)

	resolvedOnTimeAt := created.Add(10 * time.Minute)
	resOnTime := mkResolved("resolved on time", &resolvedOnTimeAt, nil)

	// A stale closed_at left over from resolving a previously-Closed ticket
	// before that bug was fixed (see R3/#208) must not leak into the SLA
	// backfill's resolution instant.
	staleClosedAt := created.Add(1 * time.Hour)
	resStaleClosed := mkResolved("resolved with stale closed_at", &resolvedLateAt, &staleClosedAt)

	// Sitting in Resolved with NO resolved_at of its own (pre-#102
	// UpdateStatus shape) — R2 must recover it from history before S1 can
	// read anything.
	resNoTicketResolvedAt := mkResolved("resolved no resolved_at", nil, nil)
	seedHistory(t, ctx, ts, resNoTicketResolvedAt.ID, nil, newSt.ID, created)
	seedHistory(t, ctx, ts, resNoTicketResolvedAt.ID, &newSt.ID, resolvedSt.ID, created.Add(20*time.Minute))

	skipAlterTable := func(stmt string) bool { return strings.HasPrefix(strings.ToUpper(stmt), "ALTER TABLE") }
	execMigrationFile(t, ctx, tx, "migrations/000027_sla_frozen_elapsed.up.sql", skipAlterTable)
	execMigrationFile(t, ctx, tx, "migrations/000028_repair_resolved_status_invariant.up.sql", nil)

	recLate, err := sls.GetRecord(ctx, resLate.ID)
	require.NoError(t, err)
	require.NotNil(t, recLate.ResolvedAt, "#238: a Resolved ticket's sla_records row must be backfilled")
	require.True(t, recLate.ResolvedAt.Equal(resolvedLateAt))
	require.NotNil(t, recLate.ResolutionElapsedAtMetSeconds)
	require.NotNil(t, recLate.ResponseElapsedAtMetSeconds)
	require.NotNil(t, recLate.FirstResponseAt)
	require.True(t, recLate.FirstResponseAt.Equal(resolvedLateAt))
	require.NotNil(t, recLate.ResolutionBreachedAt)
	require.True(t, recLate.ResolutionBreachedAt.Equal(resolvedLateAt))
	require.NotNil(t, recLate.ResponseBreachedAt)
	require.True(t, recLate.ResponseBreachedAt.Equal(resolvedLateAt))

	recOnTime, err := sls.GetRecord(ctx, resOnTime.ID)
	require.NoError(t, err)
	require.NotNil(t, recOnTime.ResolvedAt)
	require.True(t, recOnTime.ResolvedAt.Equal(resolvedOnTimeAt))
	require.Nil(t, recOnTime.ResolutionBreachedAt)
	require.Nil(t, recOnTime.ResponseBreachedAt)

	tkStaleClosedAfter, err := ts.GetByID(ctx, resStaleClosed.ID)
	require.NoError(t, err)
	require.Nil(t, tkStaleClosedAfter.ClosedAt, "R3: a Resolved ticket's stale closed_at must be cleared")
	recStaleClosed, err := sls.GetRecord(ctx, resStaleClosed.ID)
	require.NoError(t, err)
	require.NotNil(t, recStaleClosed.ResolvedAt)
	require.True(t, recStaleClosed.ResolvedAt.Equal(resolvedLateAt), "the backfill must read the ticket's own resolved_at, not its stale closed_at")

	tkNoResolvedAtAfter, err := ts.GetByID(ctx, resNoTicketResolvedAt.ID)
	require.NoError(t, err)
	require.NotNil(t, tkNoResolvedAtAfter.ResolvedAt, "R2: a Resolved ticket with no resolved_at must have one recovered from history")
	require.True(t, tkNoResolvedAtAfter.ResolvedAt.Equal(created.Add(20*time.Minute)))
	recNoResolvedAt, err := sls.GetRecord(ctx, resNoTicketResolvedAt.ID)
	require.NoError(t, err)
	require.NotNil(t, recNoResolvedAt.ResolvedAt)
	require.True(t, recNoResolvedAt.ResolvedAt.Equal(created.Add(20*time.Minute)))
	require.Nil(t, recNoResolvedAt.ResolutionBreachedAt)
	require.Nil(t, recNoResolvedAt.ResponseBreachedAt)

	// None of these Resolved tickets should ever be picked up by the live
	// breach sweep: the backfill above must have given each of them a
	// resolved_at/first_response_at (real or recovered) before the sweep's
	// first tick, so it never falsely stamps a breach dated at that tick.
	candidates, err := sls.ListBreachCandidates(ctx, now)
	require.NoError(t, err)
	seeded := map[uuid.UUID]bool{
		resLate.ID: true, resOnTime.ID: true, resStaleClosed.ID: true, resNoTicketResolvedAt.ID: true,
	}
	for _, id := range candidates {
		require.False(t, seeded[id], "a Resolved ticket the backfill already reached must not be a live breach candidate")
	}
}

// TestMigration_ClearsStaleClosedAtOnReopenedTickets pins #239: a ticket
// moved off Closed by pre-fix UpdateStatus (or any other second door) without
// clearing closed_at must have it cleared by R3, so
// ListSLABreachCandidates/ListResolvedTicketsBefore/the guest-token lookups
// all see it again once it is genuinely reopened.
func TestMigration_ClearsStaleClosedAtOnReopenedTickets(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()

	tx, err := db.SQL.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	q := dbgen.New(tx)

	us := userstore.New(q)
	cs := categorystore.New(q)
	ts := ticketstore.New(q)
	sls := slastore.New(q)

	reporter := user.User{
		ID: uuid.New(), Email: "backfill-239-" + uuid.NewString() + "@test.local",
		DisplayName: "Reporter", Role: user.RoleUser,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, reporter))
	cat := category.Category{ID: uuid.New(), Name: "Backfill 239 " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))
	inProgressSt, err := ts.GetStatusByName(ctx, "In Progress")
	require.NoError(t, err)
	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)

	var customStatusID uuid.UUID
	require.NoError(t, tx.QueryRowContext(ctx,
		`INSERT INTO statuses (name, kind, sort_order, color) VALUES ($1, 'custom', 51, '#000000') RETURNING id`,
		"Mig239 custom "+uuid.NewString()[:8]).Scan(&customStatusID))

	// A wide policy: 30 minutes response, 480 minutes resolution — this
	// ticket is 4 hours old, so it is well past the response target but
	// nowhere near the resolution one, which is what makes it a breach
	// CANDIDATE (response side outstanding) rather than an already-decided
	// breach.
	policy := sla.Policy{ID: uuid.New(), Name: "Backfill 239 policy", ResponseTargetMin: 30, ResolutionTargetMin: 480}
	require.NoError(t, sls.CreatePolicy(ctx, policy))

	now := time.Now().UTC().Truncate(time.Millisecond)
	created := now.Add(-4 * time.Hour)
	staleClosedAt := now.Add(-2 * time.Hour)

	mkReopened := func(subject string, statusID uuid.UUID) ticket.Ticket {
		tk := ticket.Ticket{
			ID: uuid.New(), TrackingNumber: ticket.TrackingNumber("BF239-" + uuid.NewString()[:8]),
			Subject: subject, Description: subject,
			CategoryID: cat.ID, Priority: ticket.PriorityMedium,
			StatusID: statusID, ReporterUserID: &reporter.ID,
			ClosedAt:  &staleClosedAt,
			CreatedAt: created, UpdatedAt: now,
		}
		require.NoError(t, ts.Create(ctx, tk))
		require.NoError(t, ts.Update(ctx, tk)) // Create never sets closed_at; write it directly.
		require.NoError(t, sls.CreateRecord(ctx, sla.Record{TicketID: tk.ID, PolicyID: policy.ID}))
		return tk
	}

	reopened := mkReopened("reopened in progress", inProgressSt.ID)
	reopenedNew := mkReopened("reopened new", newSt.ID)
	reopenedCustom := mkReopened("reopened custom", customStatusID)

	skipAlterTable := func(stmt string) bool { return strings.HasPrefix(strings.ToUpper(stmt), "ALTER TABLE") }
	execMigrationFile(t, ctx, tx, "migrations/000027_sla_frozen_elapsed.up.sql", skipAlterTable)
	execMigrationFile(t, ctx, tx, "migrations/000028_repair_resolved_status_invariant.up.sql", nil)

	for _, tk := range []ticket.Ticket{reopened, reopenedNew, reopenedCustom} {
		after, err := ts.GetByID(ctx, tk.ID)
		require.NoError(t, err, tk.Subject)
		require.Nil(t, after.ClosedAt, "#239: %s must have its stale closed_at cleared", tk.Subject)

		rec, err := sls.GetRecord(ctx, tk.ID)
		require.NoError(t, err, tk.Subject)
		require.Nil(t, rec.ResolvedAt, tk.Subject)
		require.Nil(t, rec.ResolutionElapsedAtMetSeconds, tk.Subject)
		require.Nil(t, rec.ResolutionBreachedAt, tk.Subject)
	}

	candidates, err := sls.ListBreachCandidates(ctx, now)
	require.NoError(t, err)
	ids := map[uuid.UUID]bool{}
	for _, id := range candidates {
		ids[id] = true
	}
	require.True(t, ids[reopened.ID], "#239: a reopened ticket must be visible to the breach sweep again (response elapsed 4h > 30m)")
	require.True(t, ids[reopenedNew.ID])
	require.True(t, ids[reopenedCustom.ID])
}

// TestMigration_BreachStampReachesRowsFrozenBy000027 pins #240: a frozen
// elapsed reading that migration 000027's OWN earlier backfill wrote (or that
// this file's own S2/S5 wrote in an earlier upgrade) must still get a breach
// stamp if it is late, even though the statement that froze it is not the one
// that stamps it. Before this fix, each freeze statement also decided its own
// breach in the same UPDATE, gated on the frozen column being NULL — so a row
// already frozen by an earlier pass, with no stamp, was never revisited.
func TestMigration_BreachStampReachesRowsFrozenBy000027(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()

	tx, err := db.SQL.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	q := dbgen.New(tx)

	us := userstore.New(q)
	cs := categorystore.New(q)
	ts := ticketstore.New(q)
	sls := slastore.New(q)

	reporter := user.User{
		ID: uuid.New(), Email: "backfill-240-" + uuid.NewString() + "@test.local",
		DisplayName: "Reporter", Role: user.RoleUser,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, reporter))
	cat := category.Category{ID: uuid.New(), Name: "Backfill 240 " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))
	closedSt, err := ts.GetStatusByName(ctx, ticket.StatusNameClosed)
	require.NoError(t, err)
	inProgressSt, err := ts.GetStatusByName(ctx, "In Progress")
	require.NoError(t, err)

	// A wide policy: 30 minutes response, 480 minutes resolution.
	policy := sla.Policy{ID: uuid.New(), Name: "Backfill 240 policy", ResponseTargetMin: 30, ResolutionTargetMin: 480}
	require.NoError(t, sls.CreatePolicy(ctx, policy))

	now := time.Now().UTC().Truncate(time.Millisecond)

	// frozenLate: rows come in with timestamps already set and breach columns
	// NULL, as if an earlier upgrade's 000027 already froze the elapsed
	// values (this ticket's OWN resolved_at/closed_at are already set, so
	// this file's own S1/S2 skip it — r.resolved_at is already NOT NULL).
	createdLate := now.Add(-24 * time.Hour)
	resolvedAtLate := now.Add(-12 * time.Hour)
	closedAtLate := now.Add(-10 * time.Hour)
	tkFrozenLate := ticket.Ticket{
		ID: uuid.New(), TrackingNumber: ticket.TrackingNumber("BF240L-" + uuid.NewString()[:8]),
		Subject: "frozen late", Description: "frozen late",
		CategoryID: cat.ID, Priority: ticket.PriorityMedium,
		StatusID: closedSt.ID, ReporterUserID: &reporter.ID,
		ResolvedAt: &resolvedAtLate, ClosedAt: &closedAtLate,
		CreatedAt: createdLate, UpdatedAt: now,
	}
	require.NoError(t, ts.Create(ctx, tkFrozenLate))
	require.NoError(t, ts.Update(ctx, tkFrozenLate))
	firstResponseAtLate := createdLate.Add(2 * time.Hour)
	require.NoError(t, sls.CreateRecord(ctx, sla.Record{
		TicketID: tkFrozenLate.ID, PolicyID: policy.ID,
		FirstResponseAt: &firstResponseAtLate, ResolvedAt: &resolvedAtLate,
	}))

	// frozenOnTime: same shape, but both instants are within target.
	tkFrozenOnTime := ticket.Ticket{
		ID: uuid.New(), TrackingNumber: ticket.TrackingNumber("BF240O-" + uuid.NewString()[:8]),
		Subject: "frozen on time", Description: "frozen on time",
		CategoryID: cat.ID, Priority: ticket.PriorityMedium,
		StatusID: closedSt.ID, ReporterUserID: &reporter.ID,
		ResolvedAt: &resolvedAtLate, ClosedAt: &closedAtLate,
		CreatedAt: createdLate, UpdatedAt: now,
	}
	require.NoError(t, ts.Create(ctx, tkFrozenOnTime))
	require.NoError(t, ts.Update(ctx, tkFrozenOnTime))
	firstResponseAtOnTime := createdLate.Add(10 * time.Minute)
	resolvedAtOnTime := createdLate.Add(4 * time.Hour)
	require.NoError(t, sls.CreateRecord(ctx, sla.Record{
		TicketID: tkFrozenOnTime.ID, PolicyID: policy.ID,
		FirstResponseAt: &firstResponseAtOnTime, ResolvedAt: &resolvedAtOnTime,
	}))

	// frozenPreStamped: as frozenLate, but already carrying a breach stamp
	// from an arbitrary earlier sweep instant — must survive untouched.
	tkFrozenPreStamped := ticket.Ticket{
		ID: uuid.New(), TrackingNumber: ticket.TrackingNumber("BF240P-" + uuid.NewString()[:8]),
		Subject: "frozen pre-stamped", Description: "frozen pre-stamped",
		CategoryID: cat.ID, Priority: ticket.PriorityMedium,
		StatusID: closedSt.ID, ReporterUserID: &reporter.ID,
		ResolvedAt: &resolvedAtLate, ClosedAt: &closedAtLate,
		CreatedAt: createdLate, UpdatedAt: now,
	}
	require.NoError(t, ts.Create(ctx, tkFrozenPreStamped))
	require.NoError(t, ts.Update(ctx, tkFrozenPreStamped))
	priorSweepInstant := createdLate.Add(6 * time.Hour)
	require.NoError(t, sls.CreateRecord(ctx, sla.Record{
		TicketID: tkFrozenPreStamped.ID, PolicyID: policy.ID,
		FirstResponseAt: &firstResponseAtLate, ResolvedAt: &resolvedAtLate,
		ResolutionBreachedAt: &priorSweepInstant,
	}))

	// frozenPauseGrown: the best-effort frozen value can only UNDERSTATE the
	// true elapsed time — a pause that happened AFTER the response landed
	// grows sla_paused_seconds, which only shrinks the (re)computed elapsed
	// reading below what it truly was at met time. Documents that this can
	// at worst miss a breach, never invent one.
	tkFrozenPauseGrown := ticket.Ticket{
		ID: uuid.New(), TrackingNumber: ticket.TrackingNumber("BF240G-" + uuid.NewString()[:8]),
		Subject: "frozen pause grown", Description: "frozen pause grown",
		CategoryID: cat.ID, Priority: ticket.PriorityMedium,
		StatusID: closedSt.ID, ReporterUserID: &reporter.ID,
		ResolvedAt: &resolvedAtOnTime, ClosedAt: &closedAtLate,
		CreatedAt:        createdLate,
		UpdatedAt:        now,
		SLAPausedSeconds: 3600, // an hour-long pause that happened AFTER the response
	}
	require.NoError(t, ts.Create(ctx, tkFrozenPauseGrown))
	require.NoError(t, ts.Update(ctx, tkFrozenPauseGrown))
	firstResponseAtPauseGrown := createdLate.Add(40 * time.Minute)
	require.NoError(t, sls.CreateRecord(ctx, sla.Record{
		TicketID: tkFrozenPauseGrown.ID, PolicyID: policy.ID,
		FirstResponseAt: &firstResponseAtPauseGrown, ResolvedAt: &resolvedAtOnTime,
	}))

	// reopenedMet: the ticket is now back open (In Progress), but its
	// sla_records row already carries a MET, LATE resolution from before it
	// was reopened. R1 clears tickets.resolved_at (the ticket is no longer
	// Resolved/Closed), but the sla_records fact — a resolution that DID
	// happen, and was late — is a fact about the past and must survive.
	resolvedAtBeforeReopen := createdLate.Add(10 * time.Hour)
	tkReopenedMet := ticket.Ticket{
		ID: uuid.New(), TrackingNumber: ticket.TrackingNumber("BF240R-" + uuid.NewString()[:8]),
		Subject: "reopened after a late resolution", Description: "reopened after a late resolution",
		CategoryID: cat.ID, Priority: ticket.PriorityMedium,
		StatusID: inProgressSt.ID, ReporterUserID: &reporter.ID,
		ResolvedAt: &resolvedAtBeforeReopen,
		CreatedAt:  createdLate, UpdatedAt: now,
	}
	require.NoError(t, ts.Create(ctx, tkReopenedMet))
	require.NoError(t, ts.Update(ctx, tkReopenedMet))
	require.NoError(t, sls.CreateRecord(ctx, sla.Record{
		TicketID: tkReopenedMet.ID, PolicyID: policy.ID,
		FirstResponseAt: &resolvedAtBeforeReopen, ResolvedAt: &resolvedAtBeforeReopen,
	}))

	skipAlterTable := func(stmt string) bool { return strings.HasPrefix(strings.ToUpper(stmt), "ALTER TABLE") }
	execMigrationFile(t, ctx, tx, "migrations/000027_sla_frozen_elapsed.up.sql", skipAlterTable)
	execMigrationFile(t, ctx, tx, "migrations/000028_repair_resolved_status_invariant.up.sql", nil)

	recLate, err := sls.GetRecord(ctx, tkFrozenLate.ID)
	require.NoError(t, err)
	require.NotNil(t, recLate.ResponseBreachedAt, "#240: a row 000027 froze but never stamped must still get a response breach")
	require.True(t, recLate.ResponseBreachedAt.Equal(firstResponseAtLate))
	require.NotNil(t, recLate.ResolutionBreachedAt, "#240: same, for the resolution side")
	require.True(t, recLate.ResolutionBreachedAt.Equal(resolvedAtLate))

	recOnTime, err := sls.GetRecord(ctx, tkFrozenOnTime.ID)
	require.NoError(t, err)
	require.Nil(t, recOnTime.ResponseBreachedAt)
	require.Nil(t, recOnTime.ResolutionBreachedAt)

	recPreStamped, err := sls.GetRecord(ctx, tkFrozenPreStamped.ID)
	require.NoError(t, err)
	require.NotNil(t, recPreStamped.ResolutionBreachedAt)
	require.True(t, recPreStamped.ResolutionBreachedAt.Equal(priorSweepInstant), "an existing stamp must never move")

	recPauseGrown, err := sls.GetRecord(ctx, tkFrozenPauseGrown.ID)
	require.NoError(t, err)
	require.NotNil(t, recPauseGrown.ResponseElapsedAtMetSeconds)
	require.Equal(t, int64(0), *recPauseGrown.ResponseElapsedAtMetSeconds,
		"a pause that grew AFTER the target was met can only understate the frozen elapsed reading, clamped at 0")
	require.Nil(t, recPauseGrown.ResponseBreachedAt)

	tkReopenedMetAfter, err := ts.GetByID(ctx, tkReopenedMet.ID)
	require.NoError(t, err)
	require.Nil(t, tkReopenedMetAfter.ResolvedAt, "R1: a ticket no longer in Resolved/Closed must have resolved_at cleared")

	recReopenedMet, err := sls.GetRecord(ctx, tkReopenedMet.ID)
	require.NoError(t, err)
	require.NotNil(t, recReopenedMet.ResolutionBreachedAt, "a met-late fact must survive the ticket being reopened")
	require.True(t, recReopenedMet.ResolutionBreachedAt.Equal(resolvedAtBeforeReopen))
}
