package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/stdlib"

	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/database/replyindex"
)

// prepareOptions are the flags of `server prepare-reply-search`.
type prepareOptions struct {
	ProgressInterval   time.Duration
	MaintenanceWorkMem string
	BatchSize          int
}

const prepareReplySearchUsage = `usage: prepare-reply-search [--progress-interval 30s] [--maintenance-work-mem 1GB] [--batch-size 1000]

Gets reply search ready on a large database before 1.3.0 starts, so it works the moment 1.3.0 does.
It needs only DATABASE_URL. The 1.3.0 image's entrypoint is already the server binary, so do NOT put
/app/server in front: the command is the first argument. Run it from the 1.3.0 image against the
1.2 database, while 1.2 is still serving (it does not record the migration, which 1.2 would refuse to
start on):

  docker run --rm --network <the network the database is on> -e DATABASE_URL=... <1.3.0 image> prepare-reply-search

Safe to interrupt (it exits 1 and the next run carries on). Exit codes: 0 ready, 1 error or interrupted,
2 bad arguments, 3 another run, or the 1.3.0 server, is already preparing reply search.`

func parsePrepareReplySearchFlags(args []string) (prepareOptions, error) {
	o := prepareOptions{ProgressInterval: 30 * time.Second, MaintenanceWorkMem: "1GB", BatchSize: 1000}
	fs := flag.NewFlagSet("prepare-reply-search", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.DurationVar(&o.ProgressInterval, "progress-interval", o.ProgressInterval, "how often to print progress")
	fs.StringVar(&o.MaintenanceWorkMem, "maintenance-work-mem", o.MaintenanceWorkMem, "maintenance_work_mem for the index builds")
	fs.IntVar(&o.BatchSize, "batch-size", o.BatchSize, "rows filled per transaction")
	if err := fs.Parse(args); err != nil {
		return o, err // flag.ErrHelp for --help
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
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(out, prepareReplySearchUsage)
		return 0
	}
	if err != nil {
		fmt.Fprintf(out, "prepare-reply-search: %v\n\n%s\n", err, prepareReplySearchUsage)
		return 2
	}
	// Only the database. config.Load wants BASE_URL, SESSION_SECRET and the rest, which
	// are a server's, and failed the command for anyone who had not copied the whole
	// environment.
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Fprintf(out, "prepare-reply-search: DATABASE_URL is not set\n\n%s\n", prepareReplySearchUsage)
		return 1
	}
	pool, err := database.New(ctx, dsn)
	if err != nil {
		fmt.Fprintf(out, "prepare-reply-search: connecting to the database: %v\n", err)
		return 1
	}
	defer pool.Close()
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()

	code, err := prepareReplySearch(ctx, db, opts, out)
	if err != nil {
		if ctx.Err() != nil {
			fmt.Fprintln(out, "Interrupted. Run the command again to carry on where it stopped.")
		} else {
			fmt.Fprintf(out, "prepare-reply-search: %v\n", err)
		}
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
	// What each build really runs with, read back from its own session: the flag
	// only raises the setting, so an administrator's larger value stays.
	p.OnBuild = func(index, maintenanceWorkMem string) {
		fmt.Fprintf(out, "building %s with maintenance_work_mem %s\n", index, maintenanceWorkMem)
	}

	fmt.Fprintln(out, "Preparing reply search. If the column and trigger are missing they are added first (the migration is not recorded, so 1.2 keeps running); then the search column of existing replies is filled and the indexes are built. Safe to interrupt; run again to carry on.")

	stop := make(chan struct{})
	defer close(stop)
	go reportIndexBuild(ctx, db, opts.ProgressInterval, out, stop)

	// The lock is taken before any DDL: a run that cannot take it touches nothing.
	res, err := p.PrepareWithSchema(ctx, ddl)
	switch {
	case err != nil:
		return 1, err
	case res.Locked:
		fmt.Fprintln(out, "Reply search is already being prepared by another process (the 1.3.0 server, or another run of this command). Nothing was done. Let it finish, or stop it, then run this again.")
		return 3, nil
	case res.Filled == 0 && res.Batches == 0 && !res.Built:
		fmt.Fprintln(out, "Reply search is already prepared; nothing to do.")
	default:
		fmt.Fprintf(out, "Filled %d replies.", res.Filled)
		if res.Built {
			fmt.Fprint(out, " Indexes built.")
		}
		fmt.Fprintln(out)
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
			err := db.QueryRowContext(ctx, `SELECT phase, blocks_done, blocks_total FROM pg_stat_progress_create_index WHERE datname = current_database() LIMIT 1`).Scan(&phase, &done, &total)
			if err == nil {
				fmt.Fprintf(out, "building an index: %s, %d of %d blocks\n", phase, done, total)
			}
		}
	}
}
