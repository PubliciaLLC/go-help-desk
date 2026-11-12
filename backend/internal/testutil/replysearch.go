package testutil

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/publiciallc/go-help-desk/backend/internal/database/replyindex"
)

// EnsureReplySearchReady runs the background fill and index build that makes
// reply search available, and waits for it. On a real instance the server does
// this after the migration; a test database has the column and the trigger but
// not the indexes, and reply search is skipped until they exist.
//
// Call it BEFORE the test opens its transaction: the index is built without
// blocking writes but waits for every open transaction, so one held by the
// caller would wait for itself. Several packages' tests may call it at once;
// the runner takes an advisory lock, so one builds and the others wait.
func EnsureReplySearchReady(t *testing.T) {
	t.Helper()
	db, closeDB := NewDB(t)
	defer closeDB()
	ctx := context.Background()
	p := replyindex.New(db.SQL, slog.New(slog.DiscardHandler))
	p.Pause = 0
	p.Retry = 200 * time.Millisecond

	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	p.Run(runCtx)
	ready, err := replyindex.Ready(ctx, db.SQL)
	if err != nil || !ready {
		t.Fatalf("reply search did not become ready: ready=%v err=%v", ready, err)
	}
}
