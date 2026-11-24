package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
)

// The sla_alerts_enabled setting is read on every tick and decides whether the
// pass announces; the pass stamps either way. A failed read skips the tick, so
// a crossing is never stamped silently because a read failed (#670).
func TestSweepSLA_ReadsTheAlertsSettingPerTickAndPassesItOn(t *testing.T) {
	for _, on := range []bool{true, false} {
		var gotAnnounce bool
		var gotNow time.Time
		now := time.Now()
		res, err := sweepSLA(context.Background(),
			func(context.Context) (bool, error) { return on, nil },
			func(_ context.Context, n time.Time, announce bool) (sla.SweepResult, error) {
				gotAnnounce, gotNow = announce, n
				return sla.SweepResult{Evaluated: 2, Stamped: 1, Announced: 1}, nil
			}, now)
		require.NoError(t, err)
		require.Equal(t, on, gotAnnounce)
		require.True(t, gotNow.Equal(now))
		require.Equal(t, sla.SweepResult{Evaluated: 2, Stamped: 1, Announced: 1}, res)
	}
}

func TestSweepSLA_AFailedSettingReadSkipsTheTick(t *testing.T) {
	boom := errors.New("settings unavailable")
	swept := 0
	res, err := sweepSLA(context.Background(),
		func(context.Context) (bool, error) { return false, boom },
		func(context.Context, time.Time, bool) (sla.SweepResult, error) {
			swept++
			return sla.SweepResult{Stamped: 9}, nil
		}, time.Now())
	require.ErrorIs(t, err, boom)
	require.Zero(t, swept, "nothing may be stamped on a tick whose setting could not be read")
	require.Equal(t, sla.SweepResult{}, res)
}

func TestSweepSLA_PropagatesTheSweepsErrorAndCounts(t *testing.T) {
	boom := errors.New("db unavailable")
	res, err := sweepSLA(context.Background(),
		func(context.Context) (bool, error) { return true, nil },
		func(context.Context, time.Time, bool) (sla.SweepResult, error) {
			return sla.SweepResult{Evaluated: 1}, boom
		}, time.Now())
	require.ErrorIs(t, err, boom)
	require.Equal(t, 1, res.Evaluated)
}

// One log line per pass carries the counts, and only when the pass did
// something worth a line.
func TestLogSLASweep(t *testing.T) {
	cases := []struct {
		name    string
		res     sla.SweepResult
		wantLog bool
	}{
		{"nothing happened", sla.SweepResult{Evaluated: 4}, false},
		{"stamped", sla.SweepResult{Evaluated: 4, Stamped: 1}, true},
		{"a backlog stamped silently", sla.SweepResult{Evaluated: 1000, Stamped: 1000, Stale: 2000}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(slog.NewJSONHandler(&buf, nil))
			logSLASweep(context.Background(), log, tc.res)
			if !tc.wantLog {
				require.Empty(t, buf.String())
				return
			}
			lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
			require.Len(t, lines, 1, "one line, not one per ticket")
			var rec map[string]any
			require.NoError(t, json.Unmarshal([]byte(lines[0]), &rec))
			require.EqualValues(t, tc.res.Evaluated, rec["evaluated"])
			require.EqualValues(t, tc.res.Stamped, rec["stamped"])
			require.EqualValues(t, tc.res.Announced, rec["announced"])
			require.EqualValues(t, tc.res.Stale, rec["stale"])
		})
	}
}
