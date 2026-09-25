package sla_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/stretchr/testify/require"
)

// fakeSLAStore is an in-memory implementation of sla.Store.
type fakeSLAStore struct {
	policies map[uuid.UUID]sla.Policy
	records  map[uuid.UUID]sla.Record
	// findPolicy returns a policy if one is set for (priority, categoryID).
	findResult *sla.Policy

	// candidates is what ListBreachCandidates returns.
	candidates []uuid.UUID

	updateRecordCalls int
	stampCalls        int
}

func newFakeSLAStore() *fakeSLAStore {
	return &fakeSLAStore{
		policies: make(map[uuid.UUID]sla.Policy),
		records:  make(map[uuid.UUID]sla.Record),
	}
}

func (f *fakeSLAStore) CreatePolicy(_ context.Context, p sla.Policy) error {
	f.policies[p.ID] = p
	return nil
}
func (f *fakeSLAStore) GetPolicy(_ context.Context, id uuid.UUID) (sla.Policy, error) {
	p, ok := f.policies[id]
	if !ok {
		return sla.Policy{}, errors.New("policy not found")
	}
	return p, nil
}
func (f *fakeSLAStore) UpdatePolicy(_ context.Context, p sla.Policy) error {
	f.policies[p.ID] = p
	return nil
}
func (f *fakeSLAStore) DeletePolicy(_ context.Context, id uuid.UUID) error {
	delete(f.policies, id)
	return nil
}
func (f *fakeSLAStore) ListPolicies(_ context.Context) ([]sla.Policy, error) {
	out := make([]sla.Policy, 0, len(f.policies))
	for _, p := range f.policies {
		out = append(out, p)
	}
	return out, nil
}
func (f *fakeSLAStore) FindPolicy(_ context.Context, _ ticket.Priority, _ uuid.UUID) (*sla.Policy, error) {
	return f.findResult, nil
}
func (f *fakeSLAStore) CreateRecord(_ context.Context, r sla.Record) error {
	f.records[r.TicketID] = r
	return nil
}
func (f *fakeSLAStore) GetRecord(_ context.Context, ticketID uuid.UUID) (sla.Record, error) {
	r, ok := f.records[ticketID]
	if !ok {
		return sla.Record{}, errors.New("record not found")
	}
	return r, nil
}
func (f *fakeSLAStore) UpdateRecord(_ context.Context, r sla.Record) error {
	f.updateRecordCalls++
	f.records[r.TicketID] = r
	return nil
}
func (f *fakeSLAStore) ListBreachCandidates(_ context.Context, _ time.Time) ([]uuid.UUID, error) {
	return f.candidates, nil
}

// StampBreaches applies the same COALESCE semantics the real query does:
// only a currently-nil column is set, and nothing else on the record changes.
func (f *fakeSLAStore) StampBreaches(_ context.Context, ticketID uuid.UUID, response, resolution *time.Time) error {
	f.stampCalls++
	r, ok := f.records[ticketID]
	if !ok {
		return nil // no record for this ticket: a silent no-op, like the real query.
	}
	if r.ResponseBreachedAt == nil {
		r.ResponseBreachedAt = response
	}
	if r.ResolutionBreachedAt == nil {
		r.ResolutionBreachedAt = resolution
	}
	f.records[ticketID] = r
	return nil
}

// fakeTickets is an in-memory sla.TicketGetter.
type fakeTickets struct {
	tickets map[uuid.UUID]ticket.Ticket
	errs    map[uuid.UUID]error
}

func newFakeTickets() *fakeTickets {
	return &fakeTickets{tickets: make(map[uuid.UUID]ticket.Ticket), errs: make(map[uuid.UUID]error)}
}

func (f *fakeTickets) GetByID(_ context.Context, id uuid.UUID) (ticket.Ticket, error) {
	if err, ok := f.errs[id]; ok {
		return ticket.Ticket{}, err
	}
	t, ok := f.tickets[id]
	if !ok {
		return ticket.Ticket{}, errors.New("ticket not found")
	}
	return t, nil
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestSLAService_AttachPolicy_NoPolicy(t *testing.T) {
	store := newFakeSLAStore()
	store.findResult = nil // no matching policy

	svc := sla.NewService(store)
	tk := ticket.Ticket{
		ID:         uuid.New(),
		CategoryID: uuid.New(),
		Priority:   ticket.PriorityMedium,
	}

	require.NoError(t, svc.AttachPolicy(context.Background(), tk))
	require.Empty(t, store.records, "no record should be created when no policy matches")
}

func TestSLAService_AttachPolicy_WithPolicy(t *testing.T) {
	store := newFakeSLAStore()
	policy := sla.Policy{
		ID:                  uuid.New(),
		Name:                "Standard",
		Priority:            prio(ticket.PriorityMedium),
		ResponseTargetMin:   60,
		ResolutionTargetMin: 480,
	}
	store.policies[policy.ID] = policy
	store.findResult = &policy

	svc := sla.NewService(store)
	tk := ticket.Ticket{
		ID:         uuid.New(),
		CategoryID: uuid.New(),
		Priority:   ticket.PriorityMedium,
	}

	require.NoError(t, svc.AttachPolicy(context.Background(), tk))
	rec, ok := store.records[tk.ID]
	require.True(t, ok, "record should be created")
	require.Equal(t, policy.ID, rec.PolicyID)
}

func TestSLAService_RecordFirstResponse_Idempotent(t *testing.T) {
	store := newFakeSLAStore()
	policyID := uuid.New()
	ticketID := uuid.New()
	firstTime := time.Now().Add(-10 * time.Minute)

	// Pre-seed a record with a first response already recorded.
	store.records[ticketID] = sla.Record{
		TicketID:        ticketID,
		PolicyID:        policyID,
		FirstResponseAt: &firstTime,
	}

	svc := sla.NewService(store)
	later := time.Now()
	require.NoError(t, svc.RecordFirstResponse(context.Background(), ticketID, later))

	// The stored timestamp must not have changed.
	rec := store.records[ticketID]
	require.Equal(t, firstTime.Unix(), rec.FirstResponseAt.Unix(), "timestamp must not be overwritten")
}

func TestSLAService_EvaluateBreaches(t *testing.T) {
	store := newFakeSLAStore()
	policyID := uuid.New()
	ticketID := uuid.New()

	policy := sla.Policy{
		ID:                  policyID,
		ResponseTargetMin:   30,
		ResolutionTargetMin: 120,
	}
	store.policies[policyID] = policy
	store.records[ticketID] = sla.Record{
		TicketID: ticketID,
		PolicyID: policyID,
	}

	createdAt := time.Now().Add(-3 * time.Hour) // ticket created 3h ago
	now := time.Now()

	tk := ticket.Ticket{
		ID:        ticketID,
		CreatedAt: createdAt,
	}

	svc := sla.NewService(store)
	require.NoError(t, svc.EvaluateBreaches(context.Background(), tk, now))

	rec := store.records[ticketID]
	require.NotNil(t, rec.ResponseBreachedAt, "response should be breached")
	require.NotNil(t, rec.ResolutionBreachedAt, "resolution should be breached")
}

// A ticket that would otherwise be breached must not be stamped while it is
// (or was) paused for long enough of its wall-clock lifetime. Same setup as
// TestSLAService_EvaluateBreaches, with the pause fields doing the work.
func TestSLAService_EvaluateBreaches_PausedTicketDoesNotBreach(t *testing.T) {
	store := newFakeSLAStore()
	policyID := uuid.New()
	ticketID := uuid.New()

	policy := sla.Policy{
		ID:                  policyID,
		ResponseTargetMin:   30,
		ResolutionTargetMin: 120,
	}
	store.policies[policyID] = policy
	store.records[ticketID] = sla.Record{
		TicketID: ticketID,
		PolicyID: policyID,
	}

	createdAt := time.Now().Add(-3 * time.Hour) // ticket created 3h ago
	now := time.Now()

	// Currently pending since shortly after creation: elapsed is frozen well
	// under either target.
	tk := ticket.Ticket{
		ID:           ticketID,
		CreatedAt:    createdAt,
		PendingSince: timePtr(createdAt.Add(10 * time.Minute)),
	}

	svc := sla.NewService(store)
	require.NoError(t, svc.EvaluateBreaches(context.Background(), tk, now))

	rec := store.records[ticketID]
	require.Nil(t, rec.ResponseBreachedAt, "paused elapsed time must not breach")
	require.Nil(t, rec.ResolutionBreachedAt, "paused elapsed time must not breach")

	// The interval since closed, but the accumulated pause covers the same
	// span: still no breach.
	tk.PendingSince = nil
	tk.SLAPausedSeconds = int64(now.Sub(createdAt.Add(10*time.Minute)) / time.Second)
	require.NoError(t, svc.EvaluateBreaches(context.Background(), tk, now))

	rec = store.records[ticketID]
	require.Nil(t, rec.ResponseBreachedAt, "a closed pause covering the same span must not breach either")
	require.Nil(t, rec.ResolutionBreachedAt)

	// Remove the pause entirely: now the ticket is genuinely breached.
	tk.SLAPausedSeconds = 0
	require.NoError(t, svc.EvaluateBreaches(context.Background(), tk, now))

	rec = store.records[ticketID]
	require.NotNil(t, rec.ResponseBreachedAt, "with the pause removed, elapsed time breaches again")
	require.NotNil(t, rec.ResolutionBreachedAt)
}

// A fresh breach must be stamped exactly once, and re-evaluating later (with
// the record now carrying the stamp) must not touch it again or write a
// second time.
func TestEvaluateBreaches_Idempotent(t *testing.T) {
	store := newFakeSLAStore()
	policyID := uuid.New()
	ticketID := uuid.New()

	store.policies[policyID] = sla.Policy{ID: policyID, ResponseTargetMin: 30, ResolutionTargetMin: 120}
	store.records[ticketID] = sla.Record{TicketID: ticketID, PolicyID: policyID}

	createdAt := time.Now().Add(-3 * time.Hour)
	now := time.Now()
	tk := ticket.Ticket{ID: ticketID, CreatedAt: createdAt}

	svc := sla.NewService(store)
	require.NoError(t, svc.EvaluateBreaches(context.Background(), tk, now))
	require.Equal(t, 1, store.stampCalls)

	first := store.records[ticketID]
	require.NotNil(t, first.ResponseBreachedAt)
	require.NotNil(t, first.ResolutionBreachedAt)

	// Evaluate again, an hour later: the stamp must not move, and the store
	// must not be written to again.
	require.NoError(t, svc.EvaluateBreaches(context.Background(), tk, now.Add(time.Hour)))
	require.Equal(t, 1, store.stampCalls, "an already-stamped record must not be written again")

	again := store.records[ticketID]
	require.True(t, first.ResponseBreachedAt.Equal(*again.ResponseBreachedAt))
	require.True(t, first.ResolutionBreachedAt.Equal(*again.ResolutionBreachedAt))
}

// The regression for the design's §4: a fresh breach must be written through
// StampBreaches, never through UpdateRecord — UpdateRecord is a full-row
// read-modify-write, and a reply landing between the sweep's read and its
// write would un-stamp first_response_at/resolved_at.
func TestEvaluateBreaches_NeverWritesThroughUpdateRecord(t *testing.T) {
	store := newFakeSLAStore()
	policyID := uuid.New()
	ticketID := uuid.New()

	store.policies[policyID] = sla.Policy{ID: policyID, ResponseTargetMin: 30, ResolutionTargetMin: 120}
	store.records[ticketID] = sla.Record{TicketID: ticketID, PolicyID: policyID}

	tk := ticket.Ticket{ID: ticketID, CreatedAt: time.Now().Add(-3 * time.Hour)}

	svc := sla.NewService(store)
	require.NoError(t, svc.EvaluateBreaches(context.Background(), tk, time.Now()))

	require.Equal(t, 0, store.updateRecordCalls, "a fresh breach must not go through UpdateRecord")
	require.Equal(t, 1, store.stampCalls)
	require.NotNil(t, store.records[ticketID].ResponseBreachedAt)
}

// A stamp, once set, is a fact about what happened: it must survive a later
// policy change or reopen. Both are the same code path (evaluate() sees a
// record whose breach columns are already set and does nothing), but the
// test exercises the two scenarios the issue names explicitly.
func TestEvaluateBreaches_StampSurvivesPolicyChange(t *testing.T) {
	store := newFakeSLAStore()
	policyID := uuid.New()
	ticketID := uuid.New()

	store.policies[policyID] = sla.Policy{ID: policyID, ResponseTargetMin: 30, ResolutionTargetMin: 120}
	store.records[ticketID] = sla.Record{TicketID: ticketID, PolicyID: policyID}

	createdAt := time.Now().Add(-3 * time.Hour)
	now := time.Now()
	tk := ticket.Ticket{ID: ticketID, CreatedAt: createdAt}

	svc := sla.NewService(store)
	require.NoError(t, svc.EvaluateBreaches(context.Background(), tk, now))
	stamped := store.records[ticketID]
	require.NotNil(t, stamped.ResponseBreachedAt)
	require.NotNil(t, stamped.ResolutionBreachedAt)

	// Policy change: raising the targets to something the ticket would never
	// have breached under must not clear the existing stamp.
	loose := store.policies[policyID]
	loose.ResponseTargetMin = 10000
	loose.ResolutionTargetMin = 10000
	store.policies[policyID] = loose

	require.NoError(t, svc.EvaluateBreaches(context.Background(), tk, now.Add(time.Minute)))
	afterPolicyChange := store.records[ticketID]
	require.True(t, stamped.ResponseBreachedAt.Equal(*afterPolicyChange.ResponseBreachedAt))
	require.True(t, stamped.ResolutionBreachedAt.Equal(*afterPolicyChange.ResolutionBreachedAt))

	// Reopen: clearing ResolvedAt on the record must not clear the stamp either.
	reopened := store.records[ticketID]
	reopened.ResolvedAt = nil
	store.records[ticketID] = reopened

	require.NoError(t, svc.EvaluateBreaches(context.Background(), tk, now.Add(2*time.Minute)))
	afterReopen := store.records[ticketID]
	require.True(t, stamped.ResponseBreachedAt.Equal(*afterReopen.ResponseBreachedAt))
	require.True(t, stamped.ResolutionBreachedAt.Equal(*afterReopen.ResolutionBreachedAt))
}

func TestSweepBreaches(t *testing.T) {
	t.Run("empty candidate set", func(t *testing.T) {
		store := newFakeSLAStore()
		svc := sla.NewService(store)

		res, err := svc.SweepBreaches(context.Background(), newFakeTickets(), time.Now())
		require.NoError(t, err)
		require.Equal(t, sla.SweepResult{}, res)
	})

	t.Run("two candidates, one past deadline and one not", func(t *testing.T) {
		store := newFakeSLAStore()
		policyID := uuid.New()
		store.policies[policyID] = sla.Policy{ID: policyID, ResponseTargetMin: 30, ResolutionTargetMin: 120}

		breachedID, freshID := uuid.New(), uuid.New()
		store.records[breachedID] = sla.Record{TicketID: breachedID, PolicyID: policyID}
		store.records[freshID] = sla.Record{TicketID: freshID, PolicyID: policyID}
		store.candidates = []uuid.UUID{breachedID, freshID}

		now := time.Now()
		tickets := newFakeTickets()
		tickets.tickets[breachedID] = ticket.Ticket{ID: breachedID, CreatedAt: now.Add(-3 * time.Hour)}
		tickets.tickets[freshID] = ticket.Ticket{ID: freshID, CreatedAt: now.Add(-5 * time.Minute)}

		svc := sla.NewService(store)
		res, err := svc.SweepBreaches(context.Background(), tickets, now)
		require.NoError(t, err)
		require.Equal(t, sla.SweepResult{Evaluated: 2, Stamped: 1}, res)
		require.NotNil(t, store.records[breachedID].ResponseBreachedAt)
		require.Nil(t, store.records[freshID].ResponseBreachedAt)
	})

	t.Run("one candidate's GetByID fails, the other is still stamped", func(t *testing.T) {
		store := newFakeSLAStore()
		policyID := uuid.New()
		store.policies[policyID] = sla.Policy{ID: policyID, ResponseTargetMin: 30, ResolutionTargetMin: 120}

		okID, missingID := uuid.New(), uuid.New()
		store.records[okID] = sla.Record{TicketID: okID, PolicyID: policyID}
		store.candidates = []uuid.UUID{okID, missingID}

		now := time.Now()
		tickets := newFakeTickets()
		tickets.tickets[okID] = ticket.Ticket{ID: okID, CreatedAt: now.Add(-3 * time.Hour)}
		boom := errors.New("ticket store unavailable")
		tickets.errs[missingID] = boom

		svc := sla.NewService(store)
		res, err := svc.SweepBreaches(context.Background(), tickets, now)
		require.ErrorIs(t, err, boom)
		require.Equal(t, 1, res.Evaluated, "only the successful load counts")
		require.Equal(t, 1, res.Stamped)
		require.NotNil(t, store.records[okID].ResponseBreachedAt)
	})

	t.Run("a stamped candidate re-listed is a no-op", func(t *testing.T) {
		store := newFakeSLAStore()
		policyID := uuid.New()
		store.policies[policyID] = sla.Policy{ID: policyID, ResponseTargetMin: 30, ResolutionTargetMin: 120}

		ticketID := uuid.New()
		now := time.Now()
		already := now.Add(-time.Minute)
		store.records[ticketID] = sla.Record{
			TicketID: ticketID, PolicyID: policyID,
			ResponseBreachedAt: &already, ResolutionBreachedAt: &already,
		}
		store.candidates = []uuid.UUID{ticketID}

		tickets := newFakeTickets()
		tickets.tickets[ticketID] = ticket.Ticket{ID: ticketID, CreatedAt: now.Add(-3 * time.Hour)}

		svc := sla.NewService(store)
		res, err := svc.SweepBreaches(context.Background(), tickets, now)
		require.NoError(t, err)
		require.Equal(t, sla.SweepResult{Evaluated: 1, Stamped: 0}, res)
		require.Zero(t, store.stampCalls, "an already-stamped record must not be written")
	})

	t.Run("cancelled context stops before touching the getter", func(t *testing.T) {
		store := newFakeSLAStore()
		ticketID := uuid.New()
		store.candidates = []uuid.UUID{ticketID}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		tickets := newFakeTickets() // no ticket registered: GetByID would error if called
		svc := sla.NewService(store)
		res, err := svc.SweepBreaches(ctx, tickets, time.Now())
		require.ErrorIs(t, err, context.Canceled)
		require.Zero(t, res.Evaluated)
	})
}

// swallowProbeStore lets a test distinguish "no SLA record" from a store failure, which
// is the whole point of these tests: the service used to treat both the same.
type swallowProbeStore struct {
	record    sla.Record
	hasRecord bool
	getErr    error
	updates   int
}

func (f *swallowProbeStore) CreatePolicy(context.Context, sla.Policy) error { return nil }
func (f *swallowProbeStore) GetPolicy(context.Context, uuid.UUID) (sla.Policy, error) {
	return sla.Policy{}, nil
}
func (f *swallowProbeStore) UpdatePolicy(context.Context, sla.Policy) error     { return nil }
func (f *swallowProbeStore) DeletePolicy(context.Context, uuid.UUID) error      { return nil }
func (f *swallowProbeStore) ListPolicies(context.Context) ([]sla.Policy, error) { return nil, nil }
func (f *swallowProbeStore) FindPolicy(context.Context, ticket.Priority, uuid.UUID) (*sla.Policy, error) {
	return nil, nil
}
func (f *swallowProbeStore) CreateRecord(context.Context, sla.Record) error { return nil }
func (f *swallowProbeStore) GetRecord(_ context.Context, _ uuid.UUID) (sla.Record, error) {
	if f.getErr != nil {
		return sla.Record{}, f.getErr
	}
	if !f.hasRecord {
		return sla.Record{}, sla.ErrNoRecord
	}
	return f.record, nil
}
func (f *swallowProbeStore) UpdateRecord(_ context.Context, r sla.Record) error {
	f.updates++
	f.record = r
	return nil
}
func (f *swallowProbeStore) ListBreachCandidates(context.Context, time.Time) ([]uuid.UUID, error) {
	return nil, nil
}
func (f *swallowProbeStore) StampBreaches(_ context.Context, _ uuid.UUID, response, resolution *time.Time) error {
	f.updates++
	if f.record.ResponseBreachedAt == nil {
		f.record.ResponseBreachedAt = response
	}
	if f.record.ResolutionBreachedAt == nil {
		f.record.ResolutionBreachedAt = resolution
	}
	return nil
}

// A transient database failure used to be indistinguishable from "this ticket
// has no SLA", so it was swallowed and the first-response timestamp was lost
// permanently — which later reads as a genuine breach.
func TestRecordFirstResponse_PropagatesStoreFailures(t *testing.T) {
	boom := errors.New("connection reset by peer")
	f := &swallowProbeStore{getErr: boom}

	err := sla.NewService(f).RecordFirstResponse(context.Background(), uuid.New(), time.Now())

	require.ErrorIs(t, err, boom, "a store failure must not be reported as success")
	require.Zero(t, f.updates)
}

func TestRecordFirstResponse_NoRecordIsNotAnError(t *testing.T) {
	f := &swallowProbeStore{hasRecord: false}

	require.NoError(t, sla.NewService(f).RecordFirstResponse(context.Background(), uuid.New(), time.Now()))
	require.Zero(t, f.updates, "nothing to update when the ticket is not under an SLA")
}

func TestEvaluateBreaches_PropagatesStoreFailures(t *testing.T) {
	boom := errors.New("connection reset by peer")
	f := &swallowProbeStore{getErr: boom}

	err := sla.NewService(f).EvaluateBreaches(context.Background(), ticket.Ticket{ID: uuid.New()}, time.Now())
	require.ErrorIs(t, err, boom)
}

// Nothing wrote ResolvedAt, so IsResolutionBreached saw NULL on every ticket
// and would have reported each one as breached once its deadline passed,
// however promptly it had been resolved.
func TestRecordResolved(t *testing.T) {
	now := time.Now()
	f := &swallowProbeStore{hasRecord: true, record: sla.Record{TicketID: uuid.New()}}

	require.NoError(t, sla.NewService(f).RecordResolved(context.Background(), f.record.TicketID, now))
	require.Equal(t, 1, f.updates)
	require.NotNil(t, f.record.ResolvedAt)
	require.True(t, f.record.ResolvedAt.Equal(now))

	// Re-resolving must not overwrite the original time.
	later := now.Add(2 * time.Hour)
	require.NoError(t, sla.NewService(f).RecordResolved(context.Background(), f.record.TicketID, later))
	require.Equal(t, 1, f.updates, "already recorded")
	require.True(t, f.record.ResolvedAt.Equal(now))
}

// A resolved ticket is judged by WHEN it was resolved, not by the clock now.
func TestIsResolutionBreached_UsesTheResolutionTime(t *testing.T) {
	created := time.Now().Add(-10 * time.Hour)
	policy := sla.Policy{ResolutionTargetMin: 60} // one hour
	deadline := created.Add(time.Hour)
	tk := ticket.Ticket{CreatedAt: created}

	onTime := deadline.Add(-10 * time.Minute)
	late := deadline.Add(10 * time.Minute)
	now := time.Now()

	require.False(t,
		sla.IsResolutionBreached(sla.Record{ResolvedAt: &onTime}, policy, tk, now),
		"resolved before the deadline is not a breach, however long ago that was")

	require.True(t,
		sla.IsResolutionBreached(sla.Record{ResolvedAt: &late}, policy, tk, now),
		"resolved after the deadline is a breach")

	require.True(t,
		sla.IsResolutionBreached(sla.Record{}, policy, tk, now),
		"unresolved past the deadline is a breach")
}

// A policy's priority was written to the database unvalidated. An unknown value
// reached the column's CHECK constraint and surfaced as a 500; a nil one is the
// catch-all tier and must be allowed through.
func TestSLAService_PolicyPriorityValidation(t *testing.T) {
	bogus := ticket.Priority("urgent")
	high := ticket.PriorityHigh

	cases := []struct {
		name     string
		priority *ticket.Priority
		wantErr  bool
	}{
		{name: "nil means any priority", priority: nil},
		{name: "a known priority", priority: &high},
		{name: "an unknown priority", priority: &bogus, wantErr: true},
		{name: "an empty priority", priority: prio(""), wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := sla.NewService(newFakeSLAStore())
			in := sla.Policy{
				Name:                "P",
				Priority:            tc.priority,
				ResponseTargetMin:   60,
				ResolutionTargetMin: 480,
			}

			created, err := svc.CreatePolicy(context.Background(), in)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.priority, created.Priority)
			}

			// Update is the second door onto the same column.
			in.ID = uuid.New()
			err = svc.UpdatePolicy(context.Background(), in)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func prio(p ticket.Priority) *ticket.Priority { return &p }
