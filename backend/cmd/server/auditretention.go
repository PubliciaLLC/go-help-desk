package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// auditRetentionFirstRun is how long after start the first sweep waits. Not
// zero: a sweep competing with migrations and the first requests is a poor
// trade. But short, because an instance restarted more often than the sweep
// interval (a rolling update, a nightly redeploy, a crash loop) never reaches
// a tick, and would otherwise never prune. (#331)
const auditRetentionFirstRun = 2 * time.Minute

// auditRetentionEvery is once a day because the window is denominated in days,
// same reasoning as the auto-close ticker — the interval is not itself a
// setting, only how long entries live is.
const auditRetentionEvery = 24 * time.Hour

// runAuditRetention is the audit-log retention sweep (#129): one run after
// firstRun, then one per every, until ctx is done. Separated from run() so the
// timing can be tested without a config or a database.
func runAuditRetention(
	ctx context.Context,
	log *slog.Logger,
	firstRun, every time.Duration,
	retentionDays func(context.Context) int,
	purge func(context.Context, time.Time) (int64, error),
) {
	first := time.NewTimer(firstRun)
	defer first.Stop()
	t := time.NewTicker(every)
	defer t.Stop()

	sweep := func() {
		// Read per run, never cached: the window is "as configured at the
		// time the sweep runs".
		days := retentionDays(ctx)
		// Forever is the default, so this is the branch most instances take.
		// Checked here rather than by not starting the goroutine, because the
		// setting is live: an operator who turns retention on gets a sweep
		// without a restart, the same way the SLA toggle works.
		//
		// <=, not ==: the service already maps non-positive to forever, but a
		// negative count here would put the cutoff in the future and delete
		// everything, so this does not rely on that.
		if days <= admin.AuditRetentionForever {
			return
		}
		cutoff := time.Now().AddDate(0, 0, -days)
		n, err := purge(ctx, cutoff)
		if err != nil {
			log.WarnContext(ctx, "purging expired audit entries failed", "deleted", n, "error", err)
		}
		if n > 0 {
			log.InfoContext(ctx, "purged expired audit entries", "count", n, "retention_days", days)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
			sweep()
		case <-t.C:
			sweep()
		}
	}
}

// emitAuditRetentionLog tells the operator which window this instance believes
// it has. Silent for the default (keep forever): that is the case nothing
// happens in, and a line on every boot of every instance is noise. It reports
// the value at boot — the setting is live, so it can change afterwards.
func emitAuditRetentionLog(log *slog.Logger, days int) {
	if days == admin.AuditRetentionForever {
		return
	}
	log.Info("audit log retention is on: entries older than the window are deleted daily",
		"retention_days", days,
		"first_sweep_after", auditRetentionFirstRun.String())
}
