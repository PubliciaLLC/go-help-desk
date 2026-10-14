package database_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
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

// ── Migration 000040: a ticket can have a superseded history of SLA records ──
//
// A priority re-match (#443b) never edits a record's facts. It sets
// superseded_at on the old row and inserts a new one. These tests run the real
// SQL against a real database: every query that addresses "the" record of a
// ticket must see only the current row, and the schema must refuse a second
// current row.

type rematchFx struct {
	ctx context.Context
	q   *dbgen.Queries
	tx  *sql.Tx
	ss  *slastore.Store
	ts  *ticketstore.Store
	cat category.Category
	rep user.User
	now time.Time
}

func newRematchFx(t *testing.T) *rematchFx {
	t.Helper()
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	t.Cleanup(rollback)

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	rep := user.User{
		ID: uuid.New(), Email: "rematch-" + uuid.NewString() + "@test.local", DisplayName: "Reporter",
		Role: user.RoleUser, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, userstore.New(q).Create(ctx, rep))
	cat := category.Category{ID: uuid.New(), Name: "Rematch " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, categorystore.New(q).CreateCategory(ctx, cat))

	return &rematchFx{ctx: ctx, q: q, tx: tx, ss: slastore.New(q), ts: ticketstore.New(q), cat: cat, rep: rep, now: now}
}

// ticket creates a ticket created age ago, optionally Pending since pendingSince
// with pausedSec already accumulated, the way applyStatusTimestamps would.
func (f *rematchFx) ticket(t *testing.T, age time.Duration, pendingSince *time.Time, pausedSec int64) ticket.Ticket {
	t.Helper()
	newSt, err := f.ts.GetStatusByName(f.ctx, ticket.StatusNameNew)
	require.NoError(t, err)
	created := f.now.Add(-age)
	tk := ticket.Ticket{
		ID: uuid.New(), TrackingNumber: ticket.TrackingNumber("RM-" + uuid.NewString()[:8]),
		Subject: "rematch", Description: "rematch", CategoryID: f.cat.ID, Priority: ticket.PriorityMedium,
		StatusID: newSt.ID, ReporterUserID: &f.rep.ID, CreatedAt: created, UpdatedAt: created,
	}
	require.NoError(t, f.ts.Create(f.ctx, tk))
	if pendingSince != nil || pausedSec > 0 {
		if pendingSince != nil {
			pendingSt, err := f.ts.GetStatusByName(f.ctx, ticket.StatusNamePending)
			require.NoError(t, err)
			tk.StatusID = pendingSt.ID
			tk.PendingSince = pendingSince
		}
		tk.SLAPausedSeconds = pausedSec
		require.NoError(t, f.ts.Update(f.ctx, tk))
	}
	return tk
}

func (f *rematchFx) policy(t *testing.T, name string, respMin, resMin int) sla.Policy {
	t.Helper()
	p := sla.Policy{ID: uuid.New(), Name: name, ResponseTargetMin: respMin, ResolutionTargetMin: resMin}
	require.NoError(t, f.ss.CreatePolicy(f.ctx, p))
	return p
}

// supersede supersedes the ticket's current record at at and returns its id.
func (f *rematchFx) supersede(t *testing.T, ticketID uuid.UUID, at time.Time) uuid.UUID {
	t.Helper()
	cur, err := f.q.GetCurrentSLARecordForUpdate(f.ctx, ticketID)
	require.NoError(t, err)
	n, err := f.q.SupersedeSLARecord(f.ctx, dbgen.SupersedeSLARecordParams{ID: cur.ID, SupersededAt: at})
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	return cur.ID
}

func (f *rematchFx) insert(t *testing.T, ticketID, policyID uuid.UUID, startedAt *time.Time, pausedBase int64) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var st sql.NullTime
	if startedAt != nil {
		st = sql.NullTime{Time: *startedAt, Valid: true}
	}
	require.NoError(t, f.q.InsertSLARecord(f.ctx, dbgen.InsertSLARecordParams{
		ID: id, TicketID: ticketID, PolicyID: policyID, StartedAt: st, PausedSecondsAtStart: pausedBase,
	}))
	return id
}

func (f *rematchFx) all(t *testing.T, ticketID uuid.UUID) map[uuid.UUID]dbgen.SlaRecord {
	t.Helper()
	rows, err := f.q.ListSLARecordsForTicket(f.ctx, ticketID)
	require.NoError(t, err)
	out := map[uuid.UUID]dbgen.SlaRecord{}
	for _, r := range rows {
		out[r.ID] = r
	}
	return out
}

// A superseded row is a historical fact. No query that addresses the ticket's
// record may read it, change it, or select the ticket because of it; the same
// statements land on the new current row.
func TestSLARecordSuperseded_IsInvisibleToEveryCurrentQuery(t *testing.T) {
	f := newRematchFx(t)
	tk := f.ticket(t, 4*time.Hour, nil, 0)
	old := f.policy(t, "old", 30, 120)
	next := f.policy(t, "next", 30, 120)
	require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: tk.ID, PolicyID: old.ID}))

	// Before superseding, the 4h-old record is a sweep candidate.
	got, err := f.ss.ListBreachCandidates(f.ctx, f.now)
	require.NoError(t, err)
	require.True(t, containsUUID(got, tk.ID), "precondition: the unsuperseded record is a candidate")

	oldID := f.supersede(t, tk.ID, f.now)
	before := f.all(t, tk.ID)[oldID]
	require.NotNil(t, before.SupersededAt)

	_, err = f.ss.GetRecord(f.ctx, tk.ID)
	require.ErrorIs(t, err, sla.ErrNoRecord)
	recs, err := f.ss.ListRecordsByTicketIDs(f.ctx, []uuid.UUID{tk.ID})
	require.NoError(t, err)
	require.Empty(t, recs)
	got, err = f.ss.ListBreachCandidates(f.ctx, f.now)
	require.NoError(t, err)
	require.False(t, containsUUID(got, tk.ID), "a superseded record is never a sweep candidate")

	// Every write is a silent no-op against the superseded row.
	late := f.now.Add(time.Minute)
	require.NoError(t, f.ss.SetFirstResponse(f.ctx, tk.ID, late, 60, 1800))
	require.NoError(t, f.ss.SetResolved(f.ctx, tk.ID, late, 60, 7200))
	require.NoError(t, f.ss.SetResolvedAndFirstResponse(f.ctx, tk.ID, late, 60, 7200, 1800))
	require.NoError(t, f.ss.StampBreaches(f.ctx, tk.ID, &late, &late))
	require.NoError(t, f.ss.UpdateRecord(f.ctx, sla.Record{TicketID: tk.ID, PolicyID: old.ID, FirstResponseAt: &late}))
	require.Equal(t, before, f.all(t, tk.ID)[oldID], "the superseded row is byte-identical after every write")

	// The new current row takes the same statements.
	started := f.now
	newID := f.insert(t, tk.ID, next.ID, &started, 0)
	require.NoError(t, f.ss.SetFirstResponse(f.ctx, tk.ID, late, 60, 1800))
	rows := f.all(t, tk.ID)
	require.NotNil(t, rows[newID].FirstResponseAt, "SetFirstResponse lands on the current row")
	require.Equal(t, before, rows[oldID], "and still leaves the superseded row alone")

	cur, err := f.ss.GetRecord(f.ctx, tk.ID)
	require.NoError(t, err)
	require.Equal(t, newID, cur.ID)
	require.Equal(t, next.ID, cur.PolicyID)
	require.NotNil(t, cur.StartedAt)
	require.True(t, cur.StartedAt.Equal(started))
	require.Nil(t, cur.SupersededAt)
	recs, err = f.ss.ListRecordsByTicketIDs(f.ctx, []uuid.UUID{tk.ID})
	require.NoError(t, err)
	require.Len(t, recs, 1)
	require.Equal(t, newID, recs[0].ID)
}

// The partial unique index makes a second current row impossible, so two
// concurrent re-matches cannot both win.
func TestSLARecordSuperseded_SecondCurrentRecordIsRefused(t *testing.T) {
	f := newRematchFx(t)
	tk := f.ticket(t, time.Hour, nil, 0)
	p := f.policy(t, "p", 30, 120)
	require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: tk.ID, PolicyID: p.ID}))

	err := f.q.InsertSLARecord(f.ctx, dbgen.InsertSLARecordParams{ID: uuid.New(), TicketID: tk.ID, PolicyID: p.ID})
	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr), "got %v", err)
	require.Equal(t, "23505", pgErr.Code)
	require.Equal(t, "sla_records_current_idx", pgErr.ConstraintName)
}

// Superseded rows still reference their policy (ON DELETE RESTRICT), so they
// count toward the in-use refusal.
func TestSLARecordSuperseded_StillCountsTowardPolicyInUse(t *testing.T) {
	f := newRematchFx(t)
	tk := f.ticket(t, time.Hour, nil, 0)
	old := f.policy(t, "old", 30, 120)
	next := f.policy(t, "next", 30, 120)
	require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: tk.ID, PolicyID: old.ID}))
	f.supersede(t, tk.ID, f.now)
	started := f.now
	f.insert(t, tk.ID, next.ID, &started, 0)

	n, err := f.q.CountSLARecordsByPolicy(f.ctx, old.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, n, "the superseded row counts")

	require.ErrorIs(t, f.ss.DeletePolicy(f.ctx, old.ID), sla.ErrPolicyInUse)
}

func TestSLARecordSuperseded_ListForTicketIsNewestFirstAndCurrentLocks(t *testing.T) {
	f := newRematchFx(t)
	tk := f.ticket(t, 4*time.Hour, nil, 0)
	other := f.ticket(t, 4*time.Hour, nil, 0)
	p := f.policy(t, "p", 30, 120)

	require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: other.ID, PolicyID: p.ID}))

	// r1 starts at creation (NULL started_at); r2 restarts at s; r3 re-matches
	// r2 while it was running, so it inherits s: a tie broken by superseded_at.
	require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: tk.ID, PolicyID: p.ID}))
	r1 := f.supersede(t, tk.ID, f.now.Add(-2*time.Hour))
	s := f.now.Add(-time.Hour)
	r2 := f.insert(t, tk.ID, p.ID, &s, 0)
	f.supersede(t, tk.ID, f.now.Add(-30*time.Minute))
	r3 := f.insert(t, tk.ID, p.ID, &s, 0)

	rows, err := f.q.ListSLARecordsForTicket(f.ctx, tk.ID)
	require.NoError(t, err)
	var ids []uuid.UUID
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	require.Equal(t, []uuid.UUID{r3, r2, r1}, ids)

	cur, err := f.q.GetCurrentSLARecordForUpdate(f.ctx, tk.ID)
	require.NoError(t, err)
	require.Equal(t, r3, cur.ID)

	// Superseding an already superseded row matches nothing.
	n, err := f.q.SupersedeSLARecord(f.ctx, dbgen.SupersedeSLARecordParams{ID: r1, SupersededAt: f.now})
	require.NoError(t, err)
	require.EqualValues(t, 0, n)

	// A ticket with no record has no current one.
	none := f.ticket(t, time.Hour, nil, 0)
	_, err = f.q.GetCurrentSLARecordForUpdate(f.ctx, none.ID)
	require.ErrorIs(t, err, sql.ErrNoRows)
}

// The sweep's prefilter must stay a superset of what Go would stamp for a
// restarted record, with and without an open Pending interval at the restart.
// Policy: response 30 min, resolution 120 min. Every ticket is 4 hours old and
// the record restarted 40 minutes before now unless the case says otherwise.
func TestSLARecordSuperseded_BreachCandidatesAreASupersetForRestartedRecords(t *testing.T) {
	min := time.Minute
	cases := []struct {
		name string
		// startedAgo is how long before now the record restarted.
		startedAgo time.Duration
		// pausedBase is the record's paused_seconds_at_start.
		pausedBase time.Duration
		// ticketPaused is the ticket's sla_paused_seconds now.
		ticketPaused time.Duration
		// pendingSince, relative to the restart (negative = before it); nil = not pending.
		pendingSince *time.Duration
		want         bool
	}{
		{name: "restarted one minute ago", startedAgo: 1 * min, want: false},
		{name: "restarted past the response target", startedAgo: 40 * min, want: true},
		{
			name: "pause before the restart does not count", startedAgo: 40 * min,
			pausedBase: 20 * min, ticketPaused: 20 * min, want: true,
		},
		{
			name: "pause after the restart is subtracted", startedAgo: 40 * min,
			pausedBase: 60 * min, ticketPaused: 80 * min, want: false, // 40 - 20 = 20 < 30
		},
		{
			name: "pending open at the restart and still open", startedAgo: 40 * min,
			pausedBase: 30 * min, pendingSince: durPtr(-30 * min), want: false,
		},
		{
			name: "pending opened before the restart and closed 5m after it", startedAgo: 40 * min,
			pausedBase: 30 * min, ticketPaused: 35 * min, want: true, // 40 - 5 = 35 > 30
		},
		{
			name: "pending opened 10m after the restart", startedAgo: 40 * min,
			pendingSince: durPtr(10 * min), want: false, // frozen at 10m
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRematchFx(t)
			started := f.now.Add(-tc.startedAgo)
			var pending *time.Time
			if tc.pendingSince != nil {
				p := started.Add(*tc.pendingSince)
				pending = &p
			}
			tk := f.ticket(t, 4*time.Hour, pending, int64(tc.ticketPaused/time.Second))
			policy := f.policy(t, "cand", 30, 120)
			f.insert(t, tk.ID, policy.ID, &started, int64(tc.pausedBase/time.Second))

			got, err := f.ss.ListBreachCandidates(f.ctx, f.now)
			require.NoError(t, err)
			require.Equal(t, tc.want, containsUUID(got, tk.ID))

			loaded, err := f.ts.GetByID(f.ctx, tk.ID)
			require.NoError(t, err)
			rec, err := f.ss.GetRecord(f.ctx, tk.ID)
			require.NoError(t, err)
			require.EqualValues(t, tc.pausedBase/time.Second, rec.PausedSecondsAtStart)
			if sla.IsResponseBreached(rec, policy, loaded, f.now) || sla.IsResolutionBreached(rec, policy, loaded, f.now) {
				require.True(t, containsUUID(got, tk.ID), "SQL candidate query missed a ticket Go would stamp")
			}
		})
	}
}

func durPtr(d time.Duration) *time.Duration { return &d }

// Down then up round-trips. The down migration restores ticket_id as the
// primary key, which it can only do by deleting the superseded history.
func TestMigration000040_DownThenUpRoundTrips(t *testing.T) {
	f := newRematchFx(t)
	tk := f.ticket(t, time.Hour, nil, 0)
	p := f.policy(t, "p", 30, 120)
	require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: tk.ID, PolicyID: p.ID}))
	f.supersede(t, tk.ID, f.now)
	started := f.now
	f.insert(t, tk.ID, p.ID, &started, 0)

	columns := func() map[string]bool {
		rows, err := f.tx.QueryContext(f.ctx,
			`SELECT column_name FROM information_schema.columns WHERE table_name = 'sla_records'`)
		require.NoError(t, err)
		defer rows.Close()
		out := map[string]bool{}
		for rows.Next() {
			var c string
			require.NoError(t, rows.Scan(&c))
			out[c] = true
		}
		require.NoError(t, rows.Err())
		return out
	}
	pkColumn := func() string {
		var c string
		require.NoError(t, f.tx.QueryRowContext(f.ctx, `
			SELECT a.attname FROM pg_index i
			JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY (i.indkey)
			WHERE i.indrelid = 'sla_records'::regclass AND i.indisprimary`).Scan(&c))
		return c
	}
	count := func() int {
		var n int
		require.NoError(t, f.tx.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM sla_records WHERE ticket_id = $1`, tk.ID).Scan(&n))
		return n
	}

	require.Equal(t, "id", pkColumn())
	require.Equal(t, 2, count())

	execMigrationFile(t, f.ctx, f.tx, "migrations/000040_sla_record_rematch.down.sql", nil)
	cols := columns()
	for _, c := range []string{"id", "started_at", "paused_seconds_at_start", "superseded_at"} {
		require.False(t, cols[c], "down drops %s", c)
	}
	require.Equal(t, "ticket_id", pkColumn())
	require.Equal(t, 1, count(), "down deletes the superseded history and keeps the current row")

	execMigrationFile(t, f.ctx, f.tx, "migrations/000040_sla_record_rematch.up.sql", nil)
	cols = columns()
	for _, c := range []string{"id", "started_at", "paused_seconds_at_start", "superseded_at"} {
		require.True(t, cols[c], "up adds %s", c)
	}
	require.Equal(t, "id", pkColumn())
	require.Equal(t, 1, count())

	cur, err := f.q.GetCurrentSLARecordForUpdate(f.ctx, tk.ID)
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, cur.ID, "the surviving row got an id")
	require.False(t, cur.StartedAt.Valid, "every row from before the migration starts at ticket creation")
	require.EqualValues(t, 0, cur.PausedSecondsAtStart)
}
