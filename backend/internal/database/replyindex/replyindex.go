// Package replyindex gets reply search ready on a database that already holds
// replies, while the server is running.
//
// Migration 000062 adds ticket_replies.body_tsv and a trigger that fills it for
// every new or edited reply, and nothing else, so it is instant at any size.
// This package does the rest, in the background:
//
//  1. fill body_tsv for the replies written before the migration, in small
//     batches (a transaction each, with a pause between), so reads and writes
//     carry on and no lock is held for long;
//  2. build the two indexes the search needs with CREATE INDEX CONCURRENTLY,
//     which blocks no write. golang-migrate cannot run it: a migration file is
//     one transaction, and CONCURRENTLY is refused inside one.
//
// Until both indexes are valid, reply search is off and the ticket list says so
// (dbgen ReplySearchReady). The indexes are built last, so a valid index means
// the column is complete.
//
// One runner at a time across replicas, by an advisory lock held for the whole
// pass: an index that is still being built is INVALID, and a second runner
// would otherwise take it for a failed build and drop it.
package replyindex

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
)

// LockKey is the advisory lock a runner holds while it works.
const LockKey int64 = 0x47484452504C59 // "GHDRPLY"

const (
	// ContentIndex is the GIN index the match query uses.
	ContentIndex = "ticket_replies_body_tsv_idx"
	// RecencyIndex lets the match query walk replies newest first and stop once
	// it has enough: for a word in a quarter of all replies that is a few
	// thousand rows, where the content index would hand back hundreds of
	// thousands to be sorted.
	RecencyIndex = "ticket_replies_created_at_idx"
)

// indexes in the order they are built. The recency index is a plain btree and
// quick; the GIN index is the long one.
var indexes = []struct{ name, create string }{
	{RecencyIndex, `CREATE INDEX CONCURRENTLY IF NOT EXISTS ` + RecencyIndex + ` ON ticket_replies (created_at DESC)`},
	{ContentIndex, `CREATE INDEX CONCURRENTLY IF NOT EXISTS ` + ContentIndex + ` ON ticket_replies USING gin (body_tsv)`},
}

// Preparer fills ticket_replies.body_tsv and builds its indexes.
type Preparer struct {
	DB  *sql.DB
	Log *slog.Logger

	// BatchSize is the rows filled in one transaction. Parsing a reply of about
	// 1.2 KB takes about a millisecond, so a batch holds its row locks and one
	// transaction open for about a second.
	BatchSize int
	// Pause is the wait between batches, so a large fill leaves the database
	// time for everything else.
	Pause time.Duration
	// ProgressEvery is how often a long fill logs how far it has got.
	ProgressEvery time.Duration
	// Retry is how long Run waits before trying again after a failure, or after
	// finding another runner holding the lock.
	Retry time.Duration

	// MaintenanceWorkMem is the maintenance_work_mem the index builds run with, in
	// a session of their own (see buildIndex). It is not a server setting.
	MaintenanceWorkMem string

	// OnBatch, if set, is called after each batch with the rows filled so far.
	// For tests.
	OnBatch func(filled int64)
	// OnBuild, if set, is called just before each index build with the index and
	// the maintenance_work_mem the building session reports. For tests.
	OnBuild func(index, maintenanceWorkMem string)
}

// New returns a Preparer with the production settings: batches of 1,000 and a
// quarter-second pause.
func New(db *sql.DB, log *slog.Logger) *Preparer {
	return &Preparer{
		DB: db, Log: log,
		BatchSize: 1000, Pause: 250 * time.Millisecond, MaintenanceWorkMem: "256MB",
		ProgressEvery: 30 * time.Second, Retry: 30 * time.Second,
	}
}

// Result says what one Prepare pass did.
type Result struct {
	// Done: reply search is ready (this pass made it so, or it already was).
	Done bool
	// Locked: another runner holds the lock; nothing was done.
	Locked  bool
	Filled  int64 // rows given a body_tsv by this pass
	Batches int
}

// Ready reports whether reply search may run.
func Ready(ctx context.Context, db *sql.DB) (bool, error) {
	ok, err := dbgen.New(db).ReplySearchReady(ctx)
	if err != nil {
		return false, fmt.Errorf("checking reply search: %w", err)
	}
	return ok, nil
}

// Prepare does one pass: fill what is left, then build what is missing. It can
// be interrupted at any point and run again; a restart resumes where the
// committed batches stopped.
func (p *Preparer) Prepare(ctx context.Context) (Result, error) {
	var res Result
	ready, err := Ready(ctx, p.DB)
	if err != nil {
		return res, err
	}
	if ready {
		res.Done = true
		return res, nil
	}

	conn, err := p.DB.Conn(ctx)
	if err != nil {
		return res, fmt.Errorf("reply search: taking a connection: %w", err)
	}
	defer conn.Close()
	var got bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, LockKey).Scan(&got); err != nil {
		return res, fmt.Errorf("reply search: taking the lock: %w", err)
	}
	if !got {
		res.Locked = true
		return res, nil
	}
	// The lock is the connection's, and goes with it; unlocking by hand first
	// keeps a pooled connection from carrying it into its next use.
	defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, LockKey) }()

	start := time.Now()
	if err := p.fill(ctx, &res); err != nil {
		return res, err
	}
	built, err := p.buildIndexes(ctx)
	if err != nil {
		return res, err
	}
	res.Done = true
	if res.Filled > 0 || built {
		p.Log.InfoContext(ctx, "reply search is ready",
			"replies_filled", res.Filled, "took", time.Since(start).Round(time.Second).String())
	}
	return res, nil
}

// blocksPerWindow is how much of the table one fill statement looks at: 100
// heap blocks, 800 KB. A batch is the first BatchSize unfilled rows in that
// window, so a window of tiny replies is still cut to a batch.
const blocksPerWindow = 100

// fillBatch fills the unfilled rows of one window of the table, in physical
// (ctid) order, and returns how many it filled, how many it selected and the
// last one. $1 and $2 bound the window ($1 exclusive), $3 is the batch size.
//
// Physical order is the point. Reply ids are random UUIDs, so walking the
// primary key visits one row per heap page and, on a table bigger than memory,
// reads a thousand pages for a batch of a thousand rows: 1.5 s, against 20 ms
// for the same rows read in page order (5,000,000 replies, a busy machine).
// A TID range scan reads only the window's pages; the rows are then updated by
// ctid, which is a direct fetch. ORDER BY ctid sorts only what the window
// holds, and the window is small.
const fillBatch = `
WITH b AS (
    SELECT ctid AS t FROM ticket_replies
    WHERE ctid > $1::text::tid AND ctid < $2::text::tid AND body_tsv IS NULL
    ORDER BY ctid LIMIT $3
), u AS (
    UPDATE ticket_replies r SET body_tsv = to_tsvector('english', r.body)
    WHERE r.ctid = ANY (ARRAY(SELECT t FROM b))
    RETURNING 1
)
SELECT (SELECT count(*) FROM u), (SELECT count(*) FROM b), (SELECT t::text FROM b ORDER BY t DESC LIMIT 1)`

// fill gives every reply without a body_tsv one, a batch at a time, then looks
// again: a row can be left behind a window that was already passed (a write that
// raced the batch updating its page), and search switches on only when the
// column is complete.
func (p *Preparer) fill(ctx context.Context, res *Result) error {
	// The planner's estimate, not count(*): counting the unfilled rows reads the
	// whole table, and this only sizes a log line. A negative estimate means the
	// table has never been analysed.
	var total int64
	if err := p.DB.QueryRowContext(ctx, `SELECT greatest(reltuples, 0)::bigint FROM pg_class WHERE oid = 'ticket_replies'::regclass`).Scan(&total); err != nil {
		return fmt.Errorf("reply search: sizing the table: %w", err)
	}
	p.Log.InfoContext(ctx, "reply search: filling the search column of replies that lack one, in the background",
		"replies_about", total, "batch", p.BatchSize)
	lastLog := time.Now()
	for {
		if err := p.fillPass(ctx, res, total, &lastLog); err != nil {
			return err
		}
		var left bool
		if err := p.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM ticket_replies WHERE body_tsv IS NULL)`).Scan(&left); err != nil {
			return fmt.Errorf("reply search: checking for replies left unfilled: %w", err)
		}
		if !left {
			return nil
		}
	}
}

// fillPass walks the table once, from the first block to the end as it is when
// the walk gets there. A fill's own new row versions are appended past the
// window, already filled, so the walk finishes.
func (p *Preparer) fillPass(ctx context.Context, res *Result, total int64, lastLog *time.Time) error {
	var block int64
	cursor := "(0,0)"
	for {
		var size int64
		if err := p.DB.QueryRowContext(ctx, `SELECT pg_relation_size('ticket_replies') / current_setting('block_size')::int`).Scan(&size); err != nil {
			return fmt.Errorf("reply search: measuring the table: %w", err)
		}
		if block >= size {
			return nil
		}
		end := block + blocksPerWindow
		var n, selected int64
		var last sql.NullString
		if err := p.DB.QueryRowContext(ctx, fillBatch, cursor, fmt.Sprintf("(%d,0)", end), p.BatchSize).Scan(&n, &selected, &last); err != nil {
			return fmt.Errorf("reply search: filling a batch: %w", err)
		}
		if selected >= int64(p.BatchSize) && last.Valid {
			// The batch filled up before the window ended: carry on from its last row.
			cursor = last.String
			if _, err := fmt.Sscanf(cursor, "(%d,", &block); err != nil {
				return fmt.Errorf("reply search: reading cursor %q: %w", cursor, err)
			}
		} else {
			block, cursor = end, fmt.Sprintf("(%d,0)", end)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if n == 0 {
			continue // an empty window cost one cheap read: no pause needed
		}
		res.Filled += n
		res.Batches++
		if p.OnBatch != nil {
			p.OnBatch(res.Filled)
		}
		if p.ProgressEvery > 0 && time.Since(*lastLog) >= p.ProgressEvery {
			*lastLog = time.Now()
			p.Log.InfoContext(ctx, "reply search: filling", "filled", res.Filled, "of_about", total)
		}
		if p.Pause > 0 {
			select {
			case <-time.After(p.Pause):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// buildIndexes builds each missing index without blocking writes, and replaces
// one a failed earlier build left INVALID: IF NOT EXISTS would skip it without a
// word, and search would never switch on.
func (p *Preparer) buildIndexes(ctx context.Context) (built bool, err error) {
	for _, ix := range indexes {
		var valid sql.NullBool
		err := p.DB.QueryRowContext(ctx, `SELECT indisvalid FROM pg_index WHERE indexrelid = to_regclass($1)`, ix.name).Scan(&valid)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return built, fmt.Errorf("reply search: checking %s: %w", ix.name, err)
		case valid.Bool:
			continue
		default:
			p.Log.WarnContext(ctx, "reply search: dropping an index a failed build left invalid", "index", ix.name)
			if _, err := p.DB.ExecContext(ctx, `DROP INDEX CONCURRENTLY IF EXISTS `+ix.name); err != nil {
				return built, fmt.Errorf("reply search: dropping the invalid %s: %w", ix.name, err)
			}
		}
		t := time.Now()
		p.Log.InfoContext(ctx, "reply search: building an index without blocking writes", "index", ix.name)
		if err := p.buildIndex(ctx, ix.name, ix.create); err != nil {
			return built, err
		}
		built = true
		p.Log.InfoContext(ctx, "reply search: index built", "index", ix.name, "took", time.Since(t).Round(time.Second).String())
	}
	return built, nil
}

// buildIndex runs one CREATE INDEX CONCURRENTLY in a session with a larger
// maintenance_work_mem. The GIN build keeps new entries in memory and merges
// them into the index each time that fills; at the default of 64 MB it merges so
// often that at 5,000,000 replies it was 14% done after 16 minutes, and with 1 GB
// it finished in 47 (not under equal load, so the exact ratio is not claimed).
//
// The setting is raised (never lowered) on one connection taken out of the pool and undone before
// it goes back. If it cannot be undone the connection is discarded, so it never
// reaches a query that did not ask for it. It is a plain session SET, not SET
// LOCAL: CONCURRENTLY is refused inside a transaction and SET LOCAL needs one.
func (p *Preparer) buildIndex(ctx context.Context, name, create string) (err error) {
	conn, err := p.DB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("reply search: taking a connection to build %s: %w", name, err)
	}
	defer func() {
		// The caller's ctx may be the thing that ended; the reset must still run.
		if _, rerr := conn.ExecContext(context.WithoutCancel(ctx), `RESET maintenance_work_mem`); rerr != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn }) // discard it
		}
		_ = conn.Close()
	}()
	if p.MaintenanceWorkMem != "" {
		// Only ever raised: an administrator who set it higher for this build keeps
		// theirs. set_config applies the value for the session (is_local false).
		if _, err := conn.ExecContext(ctx, `SELECT set_config('maintenance_work_mem', $1, false)
			WHERE pg_size_bytes($1) > pg_size_bytes(current_setting('maintenance_work_mem'))`, p.MaintenanceWorkMem); err != nil {
			return fmt.Errorf("reply search: setting maintenance_work_mem to %q for %s: %w", p.MaintenanceWorkMem, name, err)
		}
	}
	if p.OnBuild != nil {
		var got string
		if err := conn.QueryRowContext(ctx, `SHOW maintenance_work_mem`).Scan(&got); err != nil {
			return fmt.Errorf("reply search: reading maintenance_work_mem: %w", err)
		}
		p.OnBuild(name, got)
	}
	// Autocommit: CONCURRENTLY is refused inside a transaction.
	if _, err := conn.ExecContext(ctx, create); err != nil {
		return fmt.Errorf("reply search: building %s: %w", name, err)
	}
	return nil
}

// Run prepares reply search and returns when it is ready or ctx ends. It is
// what the server starts in the background. It retries after a failure, and
// while another replica is doing the work.
func (p *Preparer) Run(ctx context.Context) {
	for {
		res, err := p.Prepare(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			p.Log.WarnContext(ctx, "reply search preparation failed; it will be tried again", "error", err, "retry_in", p.Retry.String())
		case res.Done:
			return
		}
		select {
		case <-time.After(p.Retry):
		case <-ctx.Done():
			return
		}
	}
}
