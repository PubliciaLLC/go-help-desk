package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// #331: the sweep used to be a bare 24h ticker, so an instance restarted more
// often than daily never reached a tick and never pruned.
func TestRunAuditRetention_SweepsShortlyAfterStartNotOnlyOnTheTicker(t *testing.T) {
	var mu sync.Mutex
	var cutoffs []time.Time
	purged := make(chan struct{}, 4)
	purge := func(_ context.Context, cutoff time.Time) (int64, error) {
		mu.Lock()
		cutoffs = append(cutoffs, cutoff)
		mu.Unlock()
		purged <- struct{}{}
		return 0, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	done := make(chan struct{})
	go func() {
		defer close(done)
		// The ticker is far longer than the test: only the delayed first run
		// can make this fire.
		runAuditRetention(ctx, log, 10*time.Millisecond, time.Hour,
			func(context.Context) int { return 30 }, purge)
	}()

	select {
	case <-purged:
	case <-time.After(5 * time.Second):
		t.Fatal("no sweep ran after the startup delay; a daily-restarted instance would never prune")
	}
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, cutoffs, 1)
	want := time.Now().AddDate(0, 0, -30)
	require.WithinDuration(t, want, cutoffs[0], time.Minute)
}

func TestRunAuditRetention_ThenKeepsSweepingOnTheTicker(t *testing.T) {
	purged := make(chan struct{}, 8)
	purge := func(context.Context, time.Time) (int64, error) { purged <- struct{}{}; return 0, nil }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	go runAuditRetention(ctx, log, 5*time.Millisecond, 20*time.Millisecond,
		func(context.Context) int { return 30 }, purge)

	for i := 0; i < 3; i++ {
		select {
		case <-purged:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d sweeps ran; the ticker stopped after the first run", i)
		}
	}
}

// Retention is off by default. A sweep with no window configured must delete
// nothing — the default behaviour must not change, however often it runs.
func TestRunAuditRetention_NeverPurgesWhenRetentionIsForever(t *testing.T) {
	for _, days := range []int{admin.AuditRetentionForever, 0, -1} {
		called := false
		purge := func(context.Context, time.Time) (int64, error) { called = true; return 0, nil }

		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		log := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
		runAuditRetention(ctx, log, 5*time.Millisecond, 10*time.Millisecond,
			func(context.Context) int { return days }, purge)
		cancel()

		require.False(t, called, "days=%d: sweep deleted data with no retention window configured", days)
	}
}

func TestAuditRetentionStartupLog(t *testing.T) {
	logged := func(days int) map[string]any {
		var buf bytes.Buffer
		emitAuditRetentionLog(slog.New(slog.NewJSONHandler(&buf, nil)), days)
		if buf.Len() == 0 {
			return nil
		}
		var out map[string]any
		require.NoError(t, json.Unmarshal(buf.Bytes(), &out))
		return out
	}

	t.Run("configured: states the window the instance believes it has", func(t *testing.T) {
		got := logged(90)
		require.NotNil(t, got, "an instance with a retention window said nothing at startup")
		require.Equal(t, "INFO", got["level"])
		require.EqualValues(t, 90, got["retention_days"])
	})

	t.Run("forever: silent, it is the default", func(t *testing.T) {
		require.Nil(t, logged(admin.AuditRetentionForever))
	})
}
