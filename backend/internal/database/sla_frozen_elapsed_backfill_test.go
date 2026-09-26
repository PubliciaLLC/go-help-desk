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
	// A ticket sitting in Resolved has that as its actual status; the fake
	// New status above is a placeholder to satisfy Create, overwritten here.
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
	require.NotNil(t, rec.ResolutionElapsedAtMetSeconds)
	require.NotNil(t, rec.FirstResponseAt, "#226(a) cascades from the newly-backfilled resolved_at")
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

	// Late: closed 4 hours after creation, well past the 30-minute target,
	// closed_at never stamped (legacy shape), never separately resolved.
	created := now.Add(-4 * time.Hour)
	tkLate := ticket.Ticket{
		ID: uuid.New(), TrackingNumber: ticket.TrackingNumber("BF235L-" + uuid.NewString()[:8]),
		Subject: "late legacy close", Description: "late legacy close",
		CategoryID: cat.ID, Priority: ticket.PriorityMedium,
		StatusID: newSt.ID, ReporterUserID: &reporter.ID,
		CreatedAt: created, UpdatedAt: now,
	}
	require.NoError(t, ts.Create(ctx, tkLate))
	tkLate.StatusID = closedSt.ID
	require.NoError(t, ts.Update(ctx, tkLate))
	require.NoError(t, sls.CreateRecord(ctx, sla.Record{TicketID: tkLate.ID, PolicyID: policy.ID}))

	// On-time control: closed 10 minutes after creation, under the 30-minute
	// target, same legacy shape otherwise.
	createdOnTime := now.Add(-10 * time.Minute)
	tkOnTime := ticket.Ticket{
		ID: uuid.New(), TrackingNumber: ticket.TrackingNumber("BF235O-" + uuid.NewString()[:8]),
		Subject: "on-time legacy close", Description: "on-time legacy close",
		CategoryID: cat.ID, Priority: ticket.PriorityMedium,
		StatusID: newSt.ID, ReporterUserID: &reporter.ID,
		CreatedAt: createdOnTime, UpdatedAt: now,
	}
	require.NoError(t, ts.Create(ctx, tkOnTime))
	tkOnTime.StatusID = closedSt.ID
	require.NoError(t, ts.Update(ctx, tkOnTime))
	require.NoError(t, sls.CreateRecord(ctx, sla.Record{TicketID: tkOnTime.ID, PolicyID: policy.ID}))

	skipAlterTable := func(stmt string) bool { return strings.HasPrefix(strings.ToUpper(stmt), "ALTER TABLE") }
	execMigrationFile(t, ctx, tx, "migrations/000027_sla_frozen_elapsed.up.sql", skipAlterTable)
	execMigrationFile(t, ctx, tx, "migrations/000028_repair_resolved_status_invariant.up.sql", nil)

	recLate, err := sls.GetRecord(ctx, tkLate.ID)
	require.NoError(t, err)
	require.NotNil(t, recLate.ResolvedAt)
	require.NotNil(t, recLate.ResolutionBreachedAt,
		"#235: a legacy ticket that really was late must have resolution_breached_at stamped by the backfill")
	require.True(t, recLate.ResolutionBreachedAt.Equal(*recLate.ResolvedAt))
	require.NotNil(t, recLate.ResponseBreachedAt,
		"#235: the #226(a) response cascade inherits the same late instant and must be stamped too")

	recOnTime, err := sls.GetRecord(ctx, tkOnTime.ID)
	require.NoError(t, err)
	require.NotNil(t, recOnTime.ResolvedAt)
	require.Nil(t, recOnTime.ResolutionBreachedAt, "control: an on-time legacy close must not be stamped as breached")
	require.Nil(t, recOnTime.ResponseBreachedAt)
}
