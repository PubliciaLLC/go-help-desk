package database_test

import (
	"context"
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
	for _, stmt := range strings.Split(stripped.String(), ";") {
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
