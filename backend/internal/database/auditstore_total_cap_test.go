package database_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// #331: the audit list's total is counted only up to audit.TotalCap, because an
// exact count over a table nobody prunes is a full scan on every page view. The
// page must not depend on that count: has_more comes from reading one row past
// the page. These tests plant real rows on either side of the cap, because the
// boundary is where a bound goes wrong.

// plantMany inserts n entries in one statement, newest first from `newest`, one
// second apart, so the order is unambiguous and a test can name the first row.
func plantMany(t *testing.T, tx *sql.Tx, entityType string, entityID uuid.UUID, action string, n int, newest time.Time) {
	t.Helper()
	_, err := tx.ExecContext(context.Background(), `
		INSERT INTO audit_log (id, entity_type, entity_id, action, created_at)
		SELECT gen_random_uuid(), $1, $2, $3, $4::timestamptz - g * interval '1 second'
		FROM generate_series(0, $5::int - 1) g`,
		entityType, entityID, action, newest, n)
	require.NoError(t, err)
}

func TestAuditStore_Search_TotalIsCapped(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	defer rollback()

	ctx := context.Background()
	aus := auditstore.New(q)
	const action = "cap_probe"
	entity := uuid.New()
	newest := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	byAction := audit.Filter{Action: action}

	planted := 0
	growTo := func(n int) {
		t.Helper()
		// Older than everything already planted, so the newest entry stays put.
		plantMany(t, tx, "ticket", entity, action, n-planted, newest.Add(-time.Duration(planted)*time.Second))
		planted = n
	}
	search := func(limit, offset int) audit.Page {
		t.Helper()
		pg, err := aus.Search(ctx, byAction, limit, offset)
		require.NoError(t, err)
		return pg
	}

	const c = audit.TotalCap

	t.Run("below the cap the total is exact", func(t *testing.T) {
		growTo(c - 1)
		pg := search(50, 0)
		require.Equal(t, c-1, pg.Total)
		require.False(t, pg.TotalCapped)
		require.Len(t, pg.Entries, 50)
		require.True(t, pg.HasMore)
	})

	t.Run("exactly the cap is exact, not capped", func(t *testing.T) {
		growTo(c)
		pg := search(50, 0)
		require.Equal(t, c, pg.Total)
		require.False(t, pg.TotalCapped, "a total equal to the cap is a true count")
		require.True(t, pg.HasMore)

		last := search(1, c-1)
		require.Len(t, last.Entries, 1)
		require.False(t, last.HasMore, "the cap-th entry is the last one")
	})

	t.Run("one past the cap reports the cap and says it is capped", func(t *testing.T) {
		growTo(c + 1)
		pg := search(50, 0)
		require.Equal(t, c, pg.Total)
		require.True(t, pg.TotalCapped)
		require.True(t, pg.HasMore)
	})

	t.Run("the pager walks past the cap on has_more, not on the total", func(t *testing.T) {
		// The cap-th entry is not the last one any more.
		pg := search(1, c-1)
		require.Len(t, pg.Entries, 1)
		require.True(t, pg.HasMore, "an entry exists after the cap-th, and the total cannot say so")

		// The last page sits beyond Total and is still served in full.
		pg = search(50, c)
		require.Len(t, pg.Entries, 1)
		require.False(t, pg.HasMore)
		require.True(t, pg.TotalCapped)
		require.Equal(t, c, pg.Total)
	})

	t.Run("far above the cap the work and the answer stay the same", func(t *testing.T) {
		growTo(c + 500)
		pg := search(50, 0)
		require.Equal(t, c, pg.Total)
		require.True(t, pg.TotalCapped)

		deep := search(50, c+400)
		require.Len(t, deep.Entries, 50)
		require.True(t, deep.HasMore)
		deep = search(50, c+450)
		require.Len(t, deep.Entries, 50)
		require.False(t, deep.HasMore, "exactly full last page")
		deep = search(50, c+500)
		require.Empty(t, deep.Entries)
		require.False(t, deep.HasMore)
	})

	t.Run("the cap does not disturb the order", func(t *testing.T) {
		pg := search(5, 0)
		require.Len(t, pg.Entries, 5)
		require.True(t, pg.TotalCapped)
		for i, e := range pg.Entries {
			require.Equal(t, newest.Add(-time.Duration(i)*time.Second), e.CreatedAt.UTC(), "entry %d", i)
		}
		next := search(2, 2)
		require.Equal(t, []uuid.UUID{pg.Entries[2].ID, pg.Entries[3].ID}, []uuid.UUID{next.Entries[0].ID, next.Entries[1].ID})
	})
}

// The bound counts what matches, not what exists: a filter or a staff scope
// that leaves few matches must give an exact small total however large the
// table is. A bounded count that dropped either would report the cap.
func TestAuditStore_Search_TotalCapAppliesAfterFiltersAndScope(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	defer rollback()

	ctx := context.Background()
	f := newFilteredFixture(t, q) // staff: assigned one ticket, their group scopes catB
	aus := auditstore.New(q)
	newest := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)

	const c = audit.TotalCap
	total := func(fl audit.Filter) (int, bool) {
		t.Helper()
		pg, err := aus.Search(ctx, fl, 50, 0)
		require.NoError(t, err)
		return pg.Total, pg.TotalCapped
	}

	// Past the cap on a ticket the staff member cannot see, in two actions.
	plantMany(t, tx, "ticket", f.strangerTicket.ID, "cap_other", c+10, newest)
	plantMany(t, tx, "ticket", f.strangerTicket.ID, "cap_wanted", c+10, newest.Add(-24*time.Hour))
	// A handful on one they can.
	plantMany(t, tx, "ticket", f.assignedTicket.ID, "cap_wanted", 7, newest)

	t.Run("an action filter is applied before the cap", func(t *testing.T) {
		plantMany(t, tx, "ticket", f.strangerTicket.ID, "cap_rare", 7, newest)
		n, capped := total(audit.Filter{Action: "cap_rare"})
		require.Equal(t, 7, n)
		require.False(t, capped)
	})

	t.Run("a time window is applied before the cap", func(t *testing.T) {
		from := newest.Add(-time.Hour) // excludes the day-old cap_wanted rows on the stranger ticket
		n, capped := total(audit.Filter{Action: "cap_wanted", From: &from})
		require.Equal(t, 7, n)
		require.False(t, capped)
	})

	t.Run("staff scope is applied before the cap", func(t *testing.T) {
		n, capped := total(audit.Filter{Action: "cap_wanted", ScopedTo: &f.staff.ID})
		require.Equal(t, 7, n, "scope must decide what is counted, or the total reports tickets they cannot see")
		require.False(t, capped)

		n, capped = total(audit.Filter{Action: "cap_wanted"})
		require.Equal(t, c, n)
		require.True(t, capped, "the same filter unscoped has more than the cap")
	})

	t.Run("scope and filter together, over the cap, are capped", func(t *testing.T) {
		plantMany(t, tx, "ticket", f.assignedTicket.ID, "cap_visible", c+3, newest)
		n, capped := total(audit.Filter{Action: "cap_visible", ScopedTo: &f.staff.ID})
		require.Equal(t, c, n)
		require.True(t, capped)

		// And the page is the visible sequence, whatever the count did.
		pg, err := aus.Search(ctx, audit.Filter{Action: "cap_visible", ScopedTo: &f.staff.ID}, 50, c-10)
		require.NoError(t, err)
		require.Len(t, pg.Entries, 13)
		require.False(t, pg.HasMore)
		for _, e := range pg.Entries {
			require.Equal(t, f.assignedTicket.ID, e.EntityID)
		}
	})

	t.Run("a scope that sees nothing counts nothing", func(t *testing.T) {
		nobody := uuid.New()
		n, capped := total(audit.Filter{Action: "cap_wanted", ScopedTo: &nobody})
		require.Zero(t, n)
		require.False(t, capped)
	})
}

// The bound is the statement's own LIMIT, not something the store trims
// afterwards: a count that read every match and then clamped the number would
// return the same answers and bound nothing. So the query is held to its
// argument directly, and the store is held to passing TotalCap+1.
func TestAuditStore_CountIsBoundedByTheStatement(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	_, tx, rollback := testutil.TxQueriesTx(t, db)
	defer rollback()

	ctx := context.Background()
	newest := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	plantMany(t, tx, "ticket", uuid.New(), "cap_stmt", 10, newest)
	action := "cap_stmt"

	t.Run("the statement stops at its cap argument", func(t *testing.T) {
		q := dbgen.New(tx)
		for _, tc := range []struct {
			cap  int32
			want int64
		}{{3, 3}, {10, 10}, {11, 10}, {1000, 10}} {
			got, err := q.CountAuditLog(ctx, dbgen.CountAuditLogParams{
				Action: sql.NullString{String: action, Valid: true}, CountCap: tc.cap,
			})
			require.NoError(t, err)
			require.Equal(t, tc.want, got, "cap %d", tc.cap)
		}
	})

	t.Run("the store asks for one more than the cap", func(t *testing.T) {
		rec := &recordingDB{DBTX: tx}
		_, err := auditstore.New(dbgen.New(rec)).Search(ctx, audit.Filter{Action: action}, 5, 0)
		require.NoError(t, err)
		require.Len(t, rec.countArgs, 1, "Search must count exactly once")
		require.Equal(t, int32(audit.TotalCap+1), rec.countArgs[0][len(rec.countArgs[0])-1])
	})
}

// recordingDB remembers the arguments of every single-row query, which is how
// dbgen runs a :one statement such as CountAuditLog.
type recordingDB struct {
	dbgen.DBTX
	countArgs [][]any
}

func (r *recordingDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	r.countArgs = append(r.countArgs, args)
	return r.DBTX.QueryRowContext(ctx, query, args...)
}
