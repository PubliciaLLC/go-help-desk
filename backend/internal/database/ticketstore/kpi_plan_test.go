package ticketstore_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// kpiPlanRecorder passes every statement through to the transaction and keeps
// the plan Postgres chose for it, one-row statements (QueryRowContext) too,
// so a test looks at the plan of the real statement the store runs and not at
// a copy of its text.
type kpiPlanRecorder struct {
	dbgen.DBTX
	tx    *sql.Tx
	plans []string
}

func (p *kpiPlanRecorder) explain(ctx context.Context, query string, args []interface{}) error {
	rows, err := p.tx.QueryContext(ctx, "EXPLAIN "+query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	var plan string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return err
		}
		plan += line + "\n"
	}
	p.plans = append(p.plans, plan)
	return rows.Err()
}

func (p *kpiPlanRecorder) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	if err := p.explain(ctx, query, args); err != nil {
		return nil, err
	}
	return p.tx.QueryContext(ctx, query, args...)
}

func (p *kpiPlanRecorder) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	// A statement that cannot be explained cannot be run either, and running it
	// gives the caller the error as a Row's own.
	_ = p.explain(ctx, query, args)
	return p.tx.QueryRowContext(ctx, query, args...)
}

// Every arm of every KPI query has to be able to use the index it was written
// for: the finished arms and the open arms tickets_done_at_idx (open is a NULL
// finish instant, which a btree holds), the created arms tickets_created_at_idx.
// A table is never planned through an index on its own at this size, so the
// test takes the alternatives away (in a transaction that rolls back) and looks
// at what the planner picks from what is left: an arm that stopped matching the
// indexed expression would plan as a Seq Scan, which is what is refused. The
// 1M-row shape of the question is TestListScale (BENCH_LIST).
func TestKPI_EveryArmCanUseItsIndex(t *testing.T) {
	f := newKPIFx(t)
	// 20,000 tickets over about 416 days, oldest first as a desk writes them,
	// 70% finished.
	_, err := f.tx.ExecContext(f.ctx, `INSERT INTO tickets
		(tracking_number, subject, category_id, status_id, reporter_user_id, description_format, source_channel, priority, created_at, resolved_at)
		SELECT 'PLAN-' || g, 'plan', $1::uuid, $2::uuid, $3::uuid, 'plain', 'web',
		       (ARRAY['critical','high','medium','low'])[1 + g % 4],
		       $4::timestamptz - (20000 - g) * interval '30 minutes',
		       CASE WHEN g % 10 < 7 THEN $4::timestamptz - (20000 - g) * interval '30 minutes' + interval '2 hours' END
		FROM generate_series(1, 20000) AS g`, f.catA.ID, f.stNew.ID, f.rep.ID, kpiNow)
	require.NoError(t, err)
	for _, setting := range []string{"enable_seqscan", "enable_bitmapscan"} {
		_, err := f.tx.ExecContext(f.ctx, `SET LOCAL `+setting+` = off`)
		require.NoError(t, err)
	}
	_, err = f.tx.ExecContext(f.ctx, `ANALYZE tickets`)
	require.NoError(t, err)

	rec := &kpiPlanRecorder{DBTX: f.tx, tx: f.tx}
	ts := ticketstore.New(dbgen.New(rec))
	_, err = ts.KPI(f.ctx, f.kpiRequest())
	require.NoError(t, err)

	// The last eight statements are the request's queries, in order; any
	// before them read the caller's groups.
	require.GreaterOrEqual(t, len(rec.plans), 8)
	plans := rec.plans[len(rec.plans)-8:]
	const done, created = "tickets_done_at_idx", "tickets_created_at_idx"
	for _, tc := range []struct {
		name string
		plan string
		want []string
	}{
		{"period measures, current", plans[0], []string{done, created}},
		{"period measures, previous", plans[1], []string{done, created}},
		{"volume by day", plans[2], []string{done, created}},
		{"created by category", plans[3], []string{created}},
		{"created by priority", plans[4], []string{created}},
		{"open by age", plans[5], []string{done}},
		{"open by assignee", plans[6], []string{done}},
		{"open by group", plans[7], []string{done}},
	} {
		for _, idx := range tc.want {
			require.Contains(t, tc.plan, idx, "%s; plan:\n%s", tc.name, tc.plan)
		}
	}
}

// Store.KPI on a real pool opens its own read-only transaction. It reads what
// has been committed, so this one runs in a database of its own.
func TestKPI_OnARealPool(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ts := ticketstore.NewWithDB(db.Queries, db.SQL)
	f := &kpiFx{t: t, ctx: context.Background()}
	req := f.kpiRequest()
	raw, err := ts.KPI(f.ctx, req)
	require.NoError(t, err)
	require.Len(t, raw.Days, 30, "an empty desk is thirty days of zeros")
	require.Zero(t, raw.Current.Created)
	require.Empty(t, raw.Ages)
}
