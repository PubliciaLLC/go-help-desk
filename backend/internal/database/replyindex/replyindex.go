// Package replyindex gets reply search ready on a database that already holds
// replies, while the server is running.
//
// Migration 000061 adds ticket_replies.body_tsv and a trigger that fills it for
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

	// BatchSize is the rows filled in one transaction. Small, so a batch holds
	// its row locks for milliseconds.
	BatchSize int
	// Pause is the wait between batches, so a large fill leaves the database
	// time for everything else.
	Pause time.Duration
	// ProgressEvery is how often a long fill logs how far it has got.
	ProgressEvery time.Duration
	// Retry is how long Run waits before trying again after a failure, or after
	// finding another runner holding the lock.
	Retry time.Duration

	// OnBatch, if set, is called after each batch with the rows filled so far.
	// For tests.
	OnBatch func(filled int64)
}

// New returns a Preparer with the production settings: batches of 5,000 and a
// quarter-second pause.
func New(db *sql.DB, log *slog.Logger) *Preparer {
	return &Preparer{
		DB: db, Log: log,
		BatchSize: 5000, Pause: 250 * time.Millisecond,
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

const fillBatch = `
WITH b AS (
    SELECT id FROM ticket_replies WHERE id > $1::uuid AND body_tsv IS NULL ORDER BY id LIMIT $2
), u AS (
    UPDATE ticket_replies r SET body_tsv = to_tsvector('english', r.body)
    FROM b WHERE r.id = b.id
    RETURNING r.id
)
SELECT (SELECT count(*) FROM u), (SELECT id::text FROM b ORDER BY id DESC LIMIT 1)`

// fill gives every reply without a body_tsv one, a batch at a time, walking the
// primary key so no batch has to look past the rows an earlier one handled.
func (p *Preparer) fill(ctx context.Context, res *Result) error {
	var total int64
	if err := p.DB.QueryRowContext(ctx, `SELECT count(*) FROM ticket_replies WHERE body_tsv IS NULL`).Scan(&total); err != nil {
		return fmt.Errorf("reply search: counting replies to fill: %w", err)
	}
	if total > 0 {
		p.Log.InfoContext(ctx, "reply search: filling the search column of existing replies in the background",
			"replies", total, "batch", p.BatchSize)
	}

	cursor := "00000000-0000-0000-0000-000000000000"
	lastLog := time.Now()
	for {
		var n int64
		var last sql.NullString
		if err := p.DB.QueryRowContext(ctx, fillBatch, cursor, p.BatchSize).Scan(&n, &last); err != nil {
			return fmt.Errorf("reply search: filling a batch: %w", err)
		}
		if !last.Valid {
			return nil // nothing past the cursor without a column
		}
		cursor = last.String
		res.Filled += n
		res.Batches++
		if p.OnBatch != nil {
			p.OnBatch(res.Filled)
		}
		if p.ProgressEvery > 0 && time.Since(lastLog) >= p.ProgressEvery {
			lastLog = time.Now()
			p.Log.InfoContext(ctx, "reply search: filling", "filled", res.Filled, "of", total)
		}
		if err := ctx.Err(); err != nil {
			return err
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
		// Autocommit: CONCURRENTLY is refused inside a transaction.
		if _, err := p.DB.ExecContext(ctx, ix.create); err != nil {
			return built, fmt.Errorf("reply search: building %s: %w", ix.name, err)
		}
		built = true
		p.Log.InfoContext(ctx, "reply search: index built", "index", ix.name, "took", time.Since(t).Round(time.Second).String())
	}
	return built, nil
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
