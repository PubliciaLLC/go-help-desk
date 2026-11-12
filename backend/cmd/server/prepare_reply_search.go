package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/stdlib"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/database/replyindex"
)

// prepareOptions are the flags of `server prepare-reply-search`.
type prepareOptions struct {
	ProgressInterval   time.Duration
	MaintenanceWorkMem string
	BatchSize          int
}

const prepareReplySearchUsage = `usage: /app/server prepare-reply-search [--progress-interval 30s] [--maintenance-work-mem 1GB] [--batch-size 1000]

Gets reply search ready on a large database before 1.3.0 starts, so search works the moment it does.
Run it against the 1.2 database while 1.2 is still serving (it does not record the migration, which
1.2 would refuse to start on), or with 1.2 stopped, which is faster and costs downtime. It is safe to
interrupt: run it again and it carries on. Exit codes: 0 ready, 1 error, 3 another run (or the 1.3.0
server) is already preparing reply search.`

func parsePrepareReplySearchFlags(args []string) (prepareOptions, error) {
	o := prepareOptions{ProgressInterval: 30 * time.Second, MaintenanceWorkMem: "1GB", BatchSize: 1000}
	fs := flag.NewFlagSet("prepare-reply-search", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.DurationVar(&o.ProgressInterval, "progress-interval", o.ProgressInterval, "how often to print progress")
	fs.StringVar(&o.MaintenanceWorkMem, "maintenance-work-mem", o.MaintenanceWorkMem, "maintenance_work_mem for the index builds")
	fs.IntVar(&o.BatchSize, "batch-size", o.BatchSize, "rows filled per transaction")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	switch {
	case fs.NArg() > 0:
		return o, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	case o.ProgressInterval <= 0:
		return o, errors.New("--progress-interval must be positive")
	case o.BatchSize <= 0:
		return o, errors.New("--batch-size must be positive")
	case o.MaintenanceWorkMem == "":
		return o, errors.New("--maintenance-work-mem must not be empty")
	}
	return o, nil
}

// prepareReplySearchCommand is the entry point from main: flags, the database
// from the server's environment, then prepareReplySearch. It returns the process
// exit code.
func prepareReplySearchCommand(ctx context.Context, args []string, out io.Writer) int {
	opts, err := parsePrepareReplySearchFlags(args)
	if err != nil {
		fmt.Fprintf(out, "prepare-reply-search: %v\n\n%s\n", err, prepareReplySearchUsage)
		return 2
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(out, "prepare-reply-search: loading config: %v\n", err)
		return 1
	}
	pool, err := database.New(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintf(out, "prepare-reply-search: connecting to the database: %v\n", err)
		return 1
	}
	defer pool.Close()
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()

	code, err := prepareReplySearch(ctx, db, opts, out)
	if err != nil {
		fmt.Fprintf(out, "prepare-reply-search: %v\n", err)
	}
	return code
}

// prepareReplySearch applies the column and trigger without recording the
// migration, then runs the same fill and index build the server runs in the
// background (replyindex.Prepare, under the same advisory lock, so the two can
// never run at once), in the foreground with progress on out.
func prepareReplySearch(ctx context.Context, db *sql.DB, opts prepareOptions, out io.Writer) (int, error) {
	ddl, err := database.ReplySearchSchemaSQL()
	if err != nil {
		return 1, err
	}
	p := replyindex.New(db, slog.New(slog.NewTextHandler(out, nil)))
	p.ProgressEvery = opts.ProgressInterval
	p.BatchSize = opts.BatchSize
	p.MaintenanceWorkMem = opts.MaintenanceWorkMem

	fmt.Fprintln(out, "Adding the search column and its trigger to ticket_replies (the migration is not recorded, so 1.2 keeps running).")
	if err := p.EnsureSchema(ctx, ddl); err != nil {
		return 1, err
	}
	fmt.Fprintf(out, "Filling the search column of existing replies, then building the indexes with maintenance_work_mem %s. Safe to interrupt; run again to carry on.\n", opts.MaintenanceWorkMem)

	stop := make(chan struct{})
	defer close(stop)
	go reportIndexBuild(ctx, db, opts.ProgressInterval, out, stop)

	res, err := p.Prepare(ctx)
	switch {
	case err != nil:
		return 1, err
	case res.Locked:
		fmt.Fprintln(out, "Reply search is already being prepared by another process (the 1.3.0 server, or another run of this command). Nothing was done.")
		return 3, nil
	case res.Filled == 0 && res.Batches == 0:
		fmt.Fprintln(out, "Reply search is already prepared; nothing to do.")
	default:
		fmt.Fprintf(out, "Filled %d replies.\n", res.Filled)
	}
	fmt.Fprintln(out, "reply search is ready. Start 1.3.0 when you like.")
	return 0, nil
}

// reportIndexBuild prints how far a CREATE INDEX CONCURRENTLY has got, which
// takes most of the time on a large table and says nothing else on its own.
func reportIndexBuild(ctx context.Context, db *sql.DB, every time.Duration, out io.Writer, stop <-chan struct{}) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-t.C:
			var phase string
			var done, total int64
			err := db.QueryRowContext(ctx, `SELECT phase, blocks_done, blocks_total FROM pg_stat_progress_create_index LIMIT 1`).Scan(&phase, &done, &total)
			if err == nil {
				fmt.Fprintf(out, "building an index: %s, %d of %d blocks\n", phase, done, total)
			}
		}
	}
}
