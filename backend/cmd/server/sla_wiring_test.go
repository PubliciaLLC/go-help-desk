package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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
// "checked live, not decided once at boot" means. err, when set, is what
// SLAEnabled returns instead, standing in for a transient store failure.
type mutableEnabler struct {
	on  bool
	err error
}

func (m *mutableEnabler) SLAEnabled(context.Context) (bool, error) { return m.on, m.err }

// ── gatedSLA ──────────────────────────────────────────────────────────────────

// The core regression for #2, narrowed by #216: with the feature off,
// AttachPolicy is a no-op that never reaches the inner service — the shape of
// the bug where an admin's Settings-page toggle attached no records and ran
// no sweep. RecordFirstResponse/RecordResolved are deliberately NOT part of
// this any more (#216): they always delegate, toggle or not, because writing
// a fact onto an sla_records row that already exists is always safe, and
// gating it used to silently drop the fact instead of deferring it.
func TestGatedSLA_AttachPolicyNoOpsWhenDisabled(t *testing.T) {
	inner := &fakeInnerSLA{}
	admin := &mutableEnabler{on: false}
	g := newGatedSLA(inner, admin, discardLogger())

	require.NoError(t, g.AttachPolicy(context.Background(), ticket.Ticket{ID: uuid.New()}))

	require.Zero(t, inner.attachCalls, "the inner service must not be called while disabled")
}

// TestGatedSLA_RecordMethodsAlwaysDelegateRegardlessOfToggle pins #216: unlike
// AttachPolicy, RecordFirstResponse/RecordResolved must reach the inner
// service whether the toggle is on or off, since a record they'd write onto
// only exists because the toggle was on when the ticket was created.
func TestGatedSLA_RecordMethodsAlwaysDelegateRegardlessOfToggle(t *testing.T) {
	for _, on := range []bool{true, false} {
		t.Run(map[bool]string{true: "toggle on", false: "toggle off"}[on], func(t *testing.T) {
			inner := &fakeInnerSLA{}
			admin := &mutableEnabler{on: on}
			g := newGatedSLA(inner, admin, discardLogger())

			require.NoError(t, g.RecordFirstResponse(context.Background(), ticket.Ticket{ID: uuid.New()}, time.Now()))
			require.NoError(t, g.RecordResolved(context.Background(), ticket.Ticket{ID: uuid.New()}, time.Now()))

			require.Equal(t, 1, inner.firstResponseCalls, "RecordFirstResponse must always delegate")
			require.Equal(t, 1, inner.resolvedCalls, "RecordResolved must always delegate")
		})
	}
}

// The other half: enabled, AttachPolicy delegates and the inner error (or
// success) passes through unchanged.
func TestGatedSLA_DelegatesWhenEnabled(t *testing.T) {
	inner := &fakeInnerSLA{}
	admin := &mutableEnabler{on: true}
	g := newGatedSLA(inner, admin, discardLogger())

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
	g := newGatedSLA(inner, admin, discardLogger())

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

// TestGatedSLA_AttachPolicyLogsReadFailureAndFailsSafe pins #216's other half:
// admin.Service.SLAEnabled's read failure must be logged at this boundary
// (domain code does not log) and must not attach a policy nobody confirmed is
// wanted.
func TestGatedSLA_AttachPolicyLogsReadFailureAndFailsSafe(t *testing.T) {
	inner := &fakeInnerSLA{}
	boom := errors.New("connection reset by peer")
	admin := &mutableEnabler{on: true, err: boom}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	g := newGatedSLA(inner, admin, logger)

	err := g.AttachPolicy(context.Background(), ticket.Ticket{ID: uuid.New()})
	require.NoError(t, err, "a read failure fails safe, not fatal, to the caller")
	require.Zero(t, inner.attachCalls, "must not attach a policy when the toggle could not be confirmed")
	require.Contains(t, buf.String(), "reading SLA enabled setting failed")
	require.Contains(t, buf.String(), "connection reset by peer")
}

// discardLogger is a *slog.Logger that writes nowhere, for tests that need a
// non-nil logger but don't care what it says.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
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
// AttachPolicy AND the sweep tick start working, together, with cfg.SLAEnabled
// never touched at all. RecordFirstResponse/RecordResolved are asserted
// separately below (#216 stopped gating them on this same toggle).
func TestSLAFullyActivatesFromTheDBSettingAloneNoEnvVar(t *testing.T) {
	inner := &fakeInnerSLA{}
	toggle := &mutableEnabler{on: false} // cfg.SLAEnabled is never set anywhere in this test
	svc := newGatedSLA(inner, toggle, discardLogger())
	enabledFn := func(ctx context.Context) bool {
		on, _ := toggle.SLAEnabled(ctx)
		return on
	}

	tk := ticket.Ticket{ID: uuid.New()}
	require.NoError(t, svc.AttachPolicy(context.Background(), tk))
	ran, _, err := runSLASweepTick(context.Background(), enabledFn,
		func(context.Context, time.Time) (sla.SweepResult, error) { return sla.SweepResult{}, nil }, time.Now())
	require.NoError(t, err)
	require.False(t, ran)
	require.Zero(t, inner.attachCalls, "AttachPolicy must not run before the admin turns the feature on")

	// The admin turns SLA tracking on via Settings — the only thing this
	// bundle wires it to. No env var, no restart, no rebuilding svc.
	toggle.on = true

	require.NoError(t, svc.AttachPolicy(context.Background(), tk))
	ran, _, err = runSLASweepTick(context.Background(), enabledFn,
		func(context.Context, time.Time) (sla.SweepResult, error) { return sla.SweepResult{}, nil }, time.Now())
	require.NoError(t, err)

	require.True(t, ran, "the sweep must run once the DB setting alone says enabled")
	require.Equal(t, 1, inner.attachCalls)

	// #216: RecordFirstResponse/RecordResolved always delegate, toggle or
	// not — proven here with the toggle back off, so this is not just the
	// enabled-path coincidentally passing.
	toggle.on = false
	require.NoError(t, svc.RecordFirstResponse(context.Background(), tk, time.Now()))
	require.NoError(t, svc.RecordResolved(context.Background(), tk, time.Now()))
	require.Equal(t, 1, inner.firstResponseCalls)
	require.Equal(t, 1, inner.resolvedCalls)
}

// tinySLAStore is a minimal in-memory sla.Store, just enough to run the real
// sla.Service (not a fake ticket.SLAService) through gatedSLA, so
// TestSLAToggleOff_StillStampsALateBreachAtRecordTime below exercises the
// actual breach-stamping logic (#217/#228), not a stand-in that only counts
// calls.
type tinySLAStore struct {
	policies map[uuid.UUID]sla.Policy
	records  map[uuid.UUID]sla.Record
}

func newTinySLAStore() *tinySLAStore {
	return &tinySLAStore{policies: map[uuid.UUID]sla.Policy{}, records: map[uuid.UUID]sla.Record{}}
}

func (s *tinySLAStore) CreatePolicy(context.Context, sla.Policy) error { return nil }
func (s *tinySLAStore) GetPolicy(_ context.Context, id uuid.UUID) (sla.Policy, error) {
	return s.policies[id], nil
}
func (s *tinySLAStore) UpdatePolicy(context.Context, sla.Policy) error     { return nil }
func (s *tinySLAStore) DeletePolicy(context.Context, uuid.UUID) error      { return nil }
func (s *tinySLAStore) ListPolicies(context.Context) ([]sla.Policy, error) { return nil, nil }
func (s *tinySLAStore) FindPolicy(context.Context, ticket.Priority, uuid.UUID) (*sla.Policy, error) {
	return nil, nil
}
func (s *tinySLAStore) CreateRecord(_ context.Context, r sla.Record) error {
	s.records[r.TicketID] = r
	return nil
}
func (s *tinySLAStore) GetRecord(_ context.Context, ticketID uuid.UUID) (sla.Record, error) {
	r, ok := s.records[ticketID]
	if !ok {
		return sla.Record{}, sla.ErrNoRecord
	}
	return r, nil
}
func (s *tinySLAStore) UpdateRecord(_ context.Context, r sla.Record) error {
	s.records[r.TicketID] = r
	return nil
}
func (s *tinySLAStore) SetResolved(_ context.Context, ticketID uuid.UUID, at time.Time, elapsedSeconds, resolutionTargetSeconds int64) error {
	r, ok := s.records[ticketID]
	if !ok {
		return nil
	}
	if r.ResolvedAt == nil {
		r.ResolvedAt = &at
		r.ResolutionElapsedAtMetSeconds = &elapsedSeconds
		if elapsedSeconds > resolutionTargetSeconds && r.ResolutionBreachedAt == nil {
			r.ResolutionBreachedAt = &at
		}
	}
	s.records[ticketID] = r
	return nil
}
func (s *tinySLAStore) SetFirstResponse(_ context.Context, ticketID uuid.UUID, at time.Time, elapsedSeconds, responseTargetSeconds int64) error {
	r, ok := s.records[ticketID]
	if !ok {
		return nil
	}
	if r.FirstResponseAt == nil {
		r.FirstResponseAt = &at
		r.ResponseElapsedAtMetSeconds = &elapsedSeconds
		if elapsedSeconds > responseTargetSeconds && r.ResponseBreachedAt == nil {
			r.ResponseBreachedAt = &at
		}
	}
	s.records[ticketID] = r
	return nil
}
func (s *tinySLAStore) SetResolvedAndFirstResponse(_ context.Context, ticketID uuid.UUID, at time.Time, elapsedSeconds, resolutionTargetSeconds, responseTargetSeconds int64) error {
	r, ok := s.records[ticketID]
	if !ok {
		return nil
	}
	if r.ResolvedAt == nil {
		r.ResolvedAt = &at
		r.ResolutionElapsedAtMetSeconds = &elapsedSeconds
		if elapsedSeconds > resolutionTargetSeconds && r.ResolutionBreachedAt == nil {
			r.ResolutionBreachedAt = &at
		}
	}
	if r.FirstResponseAt == nil {
		r.FirstResponseAt = &at
		r.ResponseElapsedAtMetSeconds = &elapsedSeconds
		if elapsedSeconds > responseTargetSeconds && r.ResponseBreachedAt == nil {
			r.ResponseBreachedAt = &at
		}
	}
	s.records[ticketID] = r
	return nil
}
func (s *tinySLAStore) ListRecordsByTicketIDs(context.Context, []uuid.UUID) ([]sla.Record, error) {
	return nil, nil
}
func (s *tinySLAStore) ListBreachCandidates(context.Context, time.Time) ([]uuid.UUID, error) {
	return nil, nil
}
func (s *tinySLAStore) StampBreaches(_ context.Context, ticketID uuid.UUID, response, resolution *time.Time) error {
	r, ok := s.records[ticketID]
	if !ok {
		return nil
	}
	if r.ResponseBreachedAt == nil {
		r.ResponseBreachedAt = response
	}
	if r.ResolutionBreachedAt == nil {
		r.ResolutionBreachedAt = resolution
	}
	s.records[ticketID] = r
	return nil
}

// TestSLAToggleOff_StillStampsALateBreachAtRecordTime pins #236. The comment
// above gatedSLA.RecordResolved/RecordFirstResponse used to justify leaving
// them ungated by pointing at the sweep staying gated ("must not stamp a new
// breach while it is off"), as if that protected the whole invariant. #217/#228
// later folded breach-stamping into these same two methods, so with the
// toggle OFF, a genuinely late resolution recorded through this exact stack —
// gatedSLA wrapping the REAL sla.Service, not a call-counting fake — still
// stamps resolution_breached_at. This is the behavior the corrected comment
// now documents as intentional, not a gap to close.
func TestSLAToggleOff_StillStampsALateBreachAtRecordTime(t *testing.T) {
	store := newTinySLAStore()
	policyID := uuid.New()
	ticketID := uuid.New()
	store.policies[policyID] = sla.Policy{ID: policyID, ResponseTargetMin: 30, ResolutionTargetMin: 60}
	store.records[ticketID] = sla.Record{TicketID: ticketID, PolicyID: policyID}

	real := sla.NewService(store)
	toggle := &mutableEnabler{on: false}
	g := newGatedSLA(real, toggle, discardLogger())

	createdAt := time.Now().Add(-2 * time.Hour)
	resolvedAt := createdAt.Add(90 * time.Minute) // later than the 60-minute resolution target
	tk := ticket.Ticket{ID: ticketID, CreatedAt: createdAt}

	require.NoError(t, g.RecordResolved(context.Background(), tk, resolvedAt))

	rec := store.records[ticketID]
	require.NotNil(t, rec.ResolutionBreachedAt,
		"a late resolution must be stamped at record time even with the SLA toggle off (#236): "+
			"the stamp is a fact about lateness, not gated on the indicator's visibility")
}
