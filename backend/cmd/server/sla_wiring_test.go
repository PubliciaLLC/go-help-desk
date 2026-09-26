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

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/stretchr/testify/require"
)

// fakeInnerSLA is a ticket.SLAService double that counts calls and can be
// told to fail, so a test can tell "gatedSLA short-circuited" from "gatedSLA
// delegated and the inner call happened to fail".
type fakeInnerSLA struct {
	attachCalls, firstResponseCalls, resolvedCalls int
	err                                            error
}

func (f *fakeInnerSLA) AttachPolicy(context.Context, ticket.Ticket) error {
	f.attachCalls++
	return f.err
}

func (f *fakeInnerSLA) RecordFirstResponse(context.Context, ticket.Ticket, time.Time) error {
	f.firstResponseCalls++
	return f.err
}

func (f *fakeInnerSLA) RecordResolved(context.Context, ticket.Ticket, time.Time) error {
	f.resolvedCalls++
	return f.err
}

// mutableEnabler is a slaEnabler whose answer can change between calls,
// standing in for the live admin-settings read gatedSLA does against
// adminSvc.SLAEnabled. Its whole point is that flipping `on` takes effect on
// the very next call, with no reconstruction of gatedSLA — that is what
// "checked live, not decided once at boot" means.
type mutableEnabler struct{ on bool }

func (m *mutableEnabler) SLAEnabled(context.Context) bool { return m.on }

// ── gatedSLA ──────────────────────────────────────────────────────────────────

// The core regression for #2: with the feature off, every method is a
// no-op that never reaches the inner service — the shape of the bug where an
// admin's Settings-page toggle attached no records and ran no sweep.
func TestGatedSLA_NoOpsWhenDisabled(t *testing.T) {
	inner := &fakeInnerSLA{}
	admin := &mutableEnabler{on: false}
	g := newGatedSLA(inner, admin)

	require.NoError(t, g.AttachPolicy(context.Background(), ticket.Ticket{ID: uuid.New()}))
	require.NoError(t, g.RecordFirstResponse(context.Background(), ticket.Ticket{ID: uuid.New()}, time.Now()))
	require.NoError(t, g.RecordResolved(context.Background(), ticket.Ticket{ID: uuid.New()}, time.Now()))

	require.Zero(t, inner.attachCalls, "the inner service must not be called while disabled")
	require.Zero(t, inner.firstResponseCalls)
	require.Zero(t, inner.resolvedCalls)
}

// The other half: enabled, every method delegates and the inner error (or
// success) passes through unchanged.
func TestGatedSLA_DelegatesWhenEnabled(t *testing.T) {
	inner := &fakeInnerSLA{}
	admin := &mutableEnabler{on: true}
	g := newGatedSLA(inner, admin)

	require.NoError(t, g.AttachPolicy(context.Background(), ticket.Ticket{ID: uuid.New()}))
	require.NoError(t, g.RecordFirstResponse(context.Background(), ticket.Ticket{ID: uuid.New()}, time.Now()))
	require.NoError(t, g.RecordResolved(context.Background(), ticket.Ticket{ID: uuid.New()}, time.Now()))

	require.Equal(t, 1, inner.attachCalls)
	require.Equal(t, 1, inner.firstResponseCalls)
	require.Equal(t, 1, inner.resolvedCalls)

	inner.err = errors.New("boom")
	require.ErrorIs(t, g.RecordResolved(context.Background(), ticket.Ticket{ID: uuid.New()}, time.Now()), inner.err,
		"a real failure from the inner service must still surface")
}

// The regression #2 is actually about: flipping the SAME underlying setting
// changes gatedSLA's behaviour on its very next call, with no restart and no
// reconstruction — proving the check is live, not a value baked in at
// construction the way cfg.SLAEnabled used to be.
func TestGatedSLA_ReadsTheSettingLiveNotOnceAtConstruction(t *testing.T) {
	inner := &fakeInnerSLA{}
	admin := &mutableEnabler{on: false}
	g := newGatedSLA(inner, admin)

	require.NoError(t, g.AttachPolicy(context.Background(), ticket.Ticket{ID: uuid.New()}))
	require.Zero(t, inner.attachCalls, "starts disabled: no-op")

	// An admin flips the Settings-page toggle on. Nothing about g is rebuilt.
	admin.on = true

	require.NoError(t, g.AttachPolicy(context.Background(), ticket.Ticket{ID: uuid.New()}))
	require.Equal(t, 1, inner.attachCalls, "the very next call must pick up the toggle with no restart")

	// And flipping it back off stops it again, just as live.
	admin.on = false
	require.NoError(t, g.AttachPolicy(context.Background(), ticket.Ticket{ID: uuid.New()}))
	require.Equal(t, 1, inner.attachCalls, "turning the toggle back off must take effect immediately too")
}

// ── loggingSLA (fix #4: the discarded error is logged at the boundary) ──────

// ticket.Service discards every SLA bookkeeping error with `_ =` (see
// service.go's RecordFirstResponse/RecordResolved call sites) because a
// metrics failure must not fail the ticket operation it describes. CLAUDE.md
// requires that domain code not log, so loggingSLA is what reports these at
// the boundary instead — this test is the proof that it actually does, for
// all three methods, which nothing previously covered.
func TestLoggingSLA_LogsDiscardedErrorsAtTheBoundary(t *testing.T) {
	boom := errors.New("connection reset by peer")
	tk := ticket.Ticket{ID: uuid.MustParse("11111111-1111-1111-1111-111111111111")}

	cases := []struct {
		name    string
		call    func(l ticket.SLAService) error
		wantMsg string
	}{
		{
			name:    "AttachPolicy",
			call:    func(l ticket.SLAService) error { return l.AttachPolicy(context.Background(), tk) },
			wantMsg: "attaching SLA policy failed",
		},
		{
			name: "RecordFirstResponse",
			call: func(l ticket.SLAService) error {
				return l.RecordFirstResponse(context.Background(), tk, time.Now())
			},
			wantMsg: "recording SLA first response failed",
		},
		{
			name: "RecordResolved",
			call: func(l ticket.SLAService) error {
				return l.RecordResolved(context.Background(), tk, time.Now())
			},
			wantMsg: "recording SLA resolution failed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, nil))
			l := newLoggingSLA(&fakeInnerSLA{err: boom}, logger)

			err := tc.call(l)
			require.ErrorIs(t, err, boom, "the error is still returned to the caller")

			var rec map[string]any
			require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec),
				"exactly one JSON log line must have been written: %s", buf.String())

			require.Equal(t, "WARN", rec["level"])
			require.True(t, strings.Contains(rec["msg"].(string), tc.wantMsg), "msg: %v", rec["msg"])
			require.Equal(t, tk.ID.String(), rec["ticket_id"])
			require.Contains(t, rec["error"], "connection reset by peer")
		})
	}
}

// A successful inner call must not log anything: only a genuine, otherwise
// silently-dropped failure is boundary-worthy.
func TestLoggingSLA_NoLogOnSuccess(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	l := newLoggingSLA(&fakeInnerSLA{}, logger)

	require.NoError(t, l.RecordResolved(context.Background(), ticket.Ticket{ID: uuid.New()}, time.Now()))
	require.Empty(t, buf.String(), "nothing to report when SLA bookkeeping succeeds")
}

// ── runSLASweepTick ───────────────────────────────────────────────────────────

// The breach-sweep goroutine used to be gated on cfg.SLAEnabled at startup —
// the other half of the same #2 bug. runSLASweepTick is what it now calls on
// every tick, reading the same live setting gatedSLA does.
func TestRunSLASweepTick_SkipsSweepWhenDisabled(t *testing.T) {
	sweptCalls := 0
	ran, res, err := runSLASweepTick(context.Background(),
		func(context.Context) bool { return false },
		func(context.Context, time.Time) (sla.SweepResult, error) {
			sweptCalls++
			return sla.SweepResult{Evaluated: 5, Stamped: 5}, nil
		},
		time.Now(),
	)

	require.NoError(t, err)
	require.False(t, ran, "a disabled tick must report that it did not run")
	require.Equal(t, sla.SweepResult{}, res)
	require.Zero(t, sweptCalls, "the sweep must never be invoked while the feature is off")
}

func TestRunSLASweepTick_RunsAndPropagatesWhenEnabled(t *testing.T) {
	want := sla.SweepResult{Evaluated: 3, Stamped: 1}
	now := time.Now()
	var gotNow time.Time

	ran, res, err := runSLASweepTick(context.Background(),
		func(context.Context) bool { return true },
		func(_ context.Context, n time.Time) (sla.SweepResult, error) {
			gotNow = n
			return want, nil
		},
		now,
	)

	require.NoError(t, err)
	require.True(t, ran)
	require.Equal(t, want, res)
	require.True(t, gotNow.Equal(now), "the sweep must run against the tick's own now, not recompute it")
}

func TestRunSLASweepTick_PropagatesSweepError(t *testing.T) {
	boom := errors.New("db unavailable")
	ran, _, err := runSLASweepTick(context.Background(),
		func(context.Context) bool { return true },
		func(context.Context, time.Time) (sla.SweepResult, error) { return sla.SweepResult{}, boom },
		time.Now(),
	)

	require.True(t, ran, "a sweep that ran and failed is still \"ran\", so its error gets logged, not swallowed")
	require.ErrorIs(t, err, boom)
}

// Same behaviour, this time proving it end to end through the real
// gatedSLA/loggingSLA stack and a live admin.Service-shaped setting, rather
// than a hand-rolled function value: flipping the DB setting is what makes
// AttachPolicy/RecordFirstResponse/RecordResolved AND the sweep tick all
// start working, together, with cfg.SLAEnabled never touched at all.
func TestSLAFullyActivatesFromTheDBSettingAloneNoEnvVar(t *testing.T) {
	inner := &fakeInnerSLA{}
	toggle := &mutableEnabler{on: false} // cfg.SLAEnabled is never set anywhere in this test
	svc := newGatedSLA(inner, toggle)

	tk := ticket.Ticket{ID: uuid.New()}
	require.NoError(t, svc.AttachPolicy(context.Background(), tk))
	require.NoError(t, svc.RecordFirstResponse(context.Background(), tk, time.Now()))
	require.NoError(t, svc.RecordResolved(context.Background(), tk, time.Now()))
	ran, _, err := runSLASweepTick(context.Background(), toggle.SLAEnabled,
		func(context.Context, time.Time) (sla.SweepResult, error) { return sla.SweepResult{}, nil }, time.Now())
	require.NoError(t, err)
	require.False(t, ran)
	require.Zero(t, inner.attachCalls+inner.firstResponseCalls+inner.resolvedCalls,
		"nothing must run before the admin turns the feature on")

	// The admin turns SLA tracking on via Settings — the only thing this
	// bundle wires it to. No env var, no restart, no rebuilding svc.
	toggle.on = true

	require.NoError(t, svc.AttachPolicy(context.Background(), tk))
	require.NoError(t, svc.RecordFirstResponse(context.Background(), tk, time.Now()))
	require.NoError(t, svc.RecordResolved(context.Background(), tk, time.Now()))
	ran, _, err = runSLASweepTick(context.Background(), toggle.SLAEnabled,
		func(context.Context, time.Time) (sla.SweepResult, error) { return sla.SweepResult{}, nil }, time.Now())
	require.NoError(t, err)

	require.True(t, ran, "the sweep must run once the DB setting alone says enabled")
	require.Equal(t, 1, inner.attachCalls)
	require.Equal(t, 1, inner.firstResponseCalls)
	require.Equal(t, 1, inner.resolvedCalls)
}
