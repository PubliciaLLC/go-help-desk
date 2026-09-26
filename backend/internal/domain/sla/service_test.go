package sla_test

import (
	"context"
	"errors"
	"fmt"
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

	updateRecordCalls     int
	setFirstResponseCalls int
	setResolvedCalls      int
	stampCalls            int
	listRecordsByIDsCalls int
	listPoliciesCalls     int
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
	f.listPoliciesCalls++
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
		// Wraps sla.ErrNoRecord, matching the real store, so callers that
		// branch on it (Service.RecordFirstResponse / RecordResolved) see the
		// same "not under an SLA" signal this fake's tests rely on.
		return sla.Record{}, fmt.Errorf("%w: %s", sla.ErrNoRecord, ticketID)
	}
	return r, nil
}
func (f *fakeSLAStore) UpdateRecord(_ context.Context, r sla.Record) error {
	f.updateRecordCalls++
	f.records[r.TicketID] = r
	return nil
}

// SetFirstResponse and SetResolved apply the same COALESCE semantics the real
// queries do: only a currently-nil pair of columns is written, and nothing
// else on the record changes — in particular, a concurrent StampBreaches call
// is never clobbered by these (see TestEvaluateBreaches_NeverWritesThroughUpdateRecord
// for the regression this protects, and TestRecordFirstResponse_SurvivesConcurrentBreachStamp
// for the equivalent check on this door).
func (f *fakeSLAStore) SetFirstResponse(_ context.Context, ticketID uuid.UUID, at time.Time, elapsedSeconds int64) error {
	f.setFirstResponseCalls++
	r, ok := f.records[ticketID]
	if !ok {
		return nil // no record for this ticket: a silent no-op, like the real query.
	}
	if r.FirstResponseAt == nil {
		r.FirstResponseAt = &at
	}
	if r.ResponseElapsedAtMetSeconds == nil {
		r.ResponseElapsedAtMetSeconds = &elapsedSeconds
	}
	f.records[ticketID] = r
	return nil
}

func (f *fakeSLAStore) SetResolved(_ context.Context, ticketID uuid.UUID, at time.Time, elapsedSeconds int64) error {
	f.setResolvedCalls++
	r, ok := f.records[ticketID]
	if !ok {
		return nil
	}
	if r.ResolvedAt == nil {
		r.ResolvedAt = &at
	}
	if r.ResolutionElapsedAtMetSeconds == nil {
		r.ResolutionElapsedAtMetSeconds = &elapsedSeconds
	}
	f.records[ticketID] = r
	return nil
}
func (f *fakeSLAStore) ListBreachCandidates(_ context.Context, _ time.Time) ([]uuid.UUID, error) {
	return f.candidates, nil
}
func (f *fakeSLAStore) ListRecordsByTicketIDs(_ context.Context, ticketIDs []uuid.UUID) ([]sla.Record, error) {
	f.listRecordsByIDsCalls++
	want := make(map[uuid.UUID]bool, len(ticketIDs))
	for _, id := range ticketIDs {
		want[id] = true
	}
	var out []sla.Record
	for id, r := range f.records {
		if want[id] {
			out = append(out, r)
		}
	}
	return out, nil
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
	frozen := int64(600)

	// Pre-seed a record with a first response already recorded.
	store.records[ticketID] = sla.Record{
		TicketID:                    ticketID,
		PolicyID:                    policyID,
		FirstResponseAt:             &firstTime,
		ResponseElapsedAtMetSeconds: &frozen,
	}

	svc := sla.NewService(store)
	tk := ticket.Ticket{ID: ticketID, CreatedAt: firstTime.Add(-time.Hour)}
	later := time.Now()
	require.NoError(t, svc.RecordFirstResponse(context.Background(), tk, later))

	// The stored timestamp and frozen elapsed reading must not have changed.
	rec := store.records[ticketID]
	require.Equal(t, firstTime.Unix(), rec.FirstResponseAt.Unix(), "timestamp must not be overwritten")
	require.Equal(t, frozen, *rec.ResponseElapsedAtMetSeconds, "frozen elapsed reading must not be overwritten")
}

// A fresh first response computes elapsed from the ticket snapshot passed in,
// and freezes it in the same call — not a separate read-then-write.
func TestSLAService_RecordFirstResponse_FreezesElapsed(t *testing.T) {
	store := newFakeSLAStore()
	policyID := uuid.New()
	ticketID := uuid.New()
	// A target comfortably above the 60-minute elapsed reading below, so
	// this test's only concern (the elapsed number itself) isn't muddied by
	// also tripping the #217 record-time breach stamp.
	store.policies[policyID] = sla.Policy{ID: policyID, ResponseTargetMin: 1000, ResolutionTargetMin: 1000}
	store.records[ticketID] = sla.Record{TicketID: ticketID, PolicyID: policyID}

	createdAt := time.Now().Add(-90 * time.Minute)
	respondedAt := createdAt.Add(60 * time.Minute)
	tk := ticket.Ticket{ID: ticketID, CreatedAt: createdAt}

	svc := sla.NewService(store)
	require.NoError(t, svc.RecordFirstResponse(context.Background(), tk, respondedAt))

	rec := store.records[ticketID]
	require.NotNil(t, rec.FirstResponseAt)
	require.True(t, rec.FirstResponseAt.Equal(respondedAt))
	require.NotNil(t, rec.ResponseElapsedAtMetSeconds)
	require.Equal(t, int64(60*60), *rec.ResponseElapsedAtMetSeconds)
}

// The regression for #184: once a target is met, its elapsed reading is
// frozen as a NUMBER and must never move again, however much pause time the
// ticket accumulates afterward. Before this fix, StatusFor recomputed
// Elapsed(t, respondedAt) against the ticket's CURRENT (not its
// at-the-time) SLAPausedSeconds, so a Pending interval that opened and
// closed AFTER the response was recorded silently shrank an already-late
// reading — flipping a breach back to green.
func TestFrozenResponseElapsed_SurvivesLaterPendingCycles(t *testing.T) {
	store := newFakeSLAStore()
	policyID := uuid.New()
	ticketID := uuid.New()
	policy := sla.Policy{ID: policyID, ResponseTargetMin: 30, ResolutionTargetMin: 1000}
	store.policies[policyID] = policy
	store.records[ticketID] = sla.Record{TicketID: ticketID, PolicyID: policyID}

	createdAt := time.Now().Add(-2 * time.Hour)
	tk := ticket.Ticket{ID: ticketID, CreatedAt: createdAt}

	// Staff replies 60 minutes in: the response target was 30 minutes, so
	// this is a breach at the moment it is recorded.
	respondedAt := createdAt.Add(60 * time.Minute)

	svc := sla.NewService(store)
	require.NoError(t, svc.RecordFirstResponse(context.Background(), tk, respondedAt))
	rec := store.records[ticketID]

	before := sla.StatusFor(rec, policy, tk, respondedAt)
	require.Equal(t, sla.Red, before.Response.Color, "sanity check: the response really was late")
	require.Equal(t, 60, before.Response.ElapsedMin)

	// The ticket goes Pending after the response was already recorded, and
	// later comes back: more paused seconds accumulate on the ticket AFTER
	// the target was met.
	tk.SLAPausedSeconds = int64(45 * time.Minute / time.Second)

	after := sla.StatusFor(rec, policy, tk, respondedAt.Add(3*time.Hour))

	require.Equal(t, before.Response.ElapsedMin, after.Response.ElapsedMin,
		"a met target's elapsed reading must never move, regardless of later pause activity")
	require.Equal(t, sla.Red, after.Response.Color,
		"a breach must not flip back to green after a later Pending cycle")
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
	setErr    error // returned by SetFirstResponse / SetResolved, independent of getErr
	updates   int
	// policy is returned by GetPolicy unconditionally (this fake has exactly
	// one ticket in play). Left zero-valued, both targets are 0 minutes, so
	// any positive elapsed reading counts as an immediate breach — tests that
	// care about NOT breaching set this explicitly.
	policy sla.Policy
}

func (f *swallowProbeStore) CreatePolicy(context.Context, sla.Policy) error { return nil }
func (f *swallowProbeStore) GetPolicy(context.Context, uuid.UUID) (sla.Policy, error) {
	return f.policy, nil
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

// SetFirstResponse and SetResolved apply the same "only write a NULL column"
// rule the real COALESCE-guarded queries do, keyed off hasRecord the same way
// GetRecord's ErrNoRecord is: a ticket with no record is a silent no-op.
func (f *swallowProbeStore) SetFirstResponse(_ context.Context, _ uuid.UUID, at time.Time, elapsedSeconds int64) error {
	if f.setErr != nil {
		return f.setErr
	}
	if !f.hasRecord {
		return nil
	}
	f.updates++
	if f.record.FirstResponseAt == nil {
		f.record.FirstResponseAt = &at
	}
	if f.record.ResponseElapsedAtMetSeconds == nil {
		f.record.ResponseElapsedAtMetSeconds = &elapsedSeconds
	}
	return nil
}

func (f *swallowProbeStore) SetResolved(_ context.Context, _ uuid.UUID, at time.Time, elapsedSeconds int64) error {
	if f.setErr != nil {
		return f.setErr
	}
	if !f.hasRecord {
		return nil
	}
	f.updates++
	if f.record.ResolvedAt == nil {
		f.record.ResolvedAt = &at
	}
	if f.record.ResolutionElapsedAtMetSeconds == nil {
		f.record.ResolutionElapsedAtMetSeconds = &elapsedSeconds
	}
	return nil
}

func (f *swallowProbeStore) ListBreachCandidates(context.Context, time.Time) ([]uuid.UUID, error) {
	return nil, nil
}
func (f *swallowProbeStore) ListRecordsByTicketIDs(context.Context, []uuid.UUID) ([]sla.Record, error) {
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
	// hasRecord: true — a store failure on the WRITE (setErr) must surface
	// even though the record read that precedes it (for the policy lookup)
	// succeeds; a missing record entirely is the separate, deliberately
	// no-op case TestRecordFirstResponse_NoRecordIsNotAnError covers.
	f := &swallowProbeStore{hasRecord: true, setErr: boom}

	tk := ticket.Ticket{ID: uuid.New(), CreatedAt: time.Now().Add(-time.Hour)}
	err := sla.NewService(f).RecordFirstResponse(context.Background(), tk, time.Now())

	require.ErrorIs(t, err, boom, "a store failure must not be reported as success")
	require.Zero(t, f.updates)
}

func TestRecordFirstResponse_NoRecordIsNotAnError(t *testing.T) {
	f := &swallowProbeStore{hasRecord: false}

	tk := ticket.Ticket{ID: uuid.New(), CreatedAt: time.Now().Add(-time.Hour)}
	require.NoError(t, sla.NewService(f).RecordFirstResponse(context.Background(), tk, time.Now()))
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
//
// #219 and #220 changed this test's shape: a resolution with no prior reply
// now ALSO backfills first_response_at (SetResolved + SetFirstResponse, two
// store writes), and RecordResolved itself now short-circuits when the record
// is already resolved (checked in the service, before touching the store
// again at all) rather than relying on the store's own COALESCE guard to make
// a second call a no-op — the service has to decide this early so it does not
// re-read the policy and re-evaluate breach state against a later "now" that
// has nothing to do with when the ticket actually resolved.
func TestRecordResolved(t *testing.T) {
	now := time.Now()
	ticketID := uuid.New()
	f := &swallowProbeStore{
		hasRecord: true,
		record:    sla.Record{TicketID: ticketID},
		// Comfortably above the 3-hour elapsed reading below, so this test's
		// own concern isn't muddied by also tripping a breach stamp.
		policy: sla.Policy{ResponseTargetMin: 1000, ResolutionTargetMin: 1000},
	}
	tk := ticket.Ticket{ID: ticketID, CreatedAt: now.Add(-3 * time.Hour)}

	require.NoError(t, sla.NewService(f).RecordResolved(context.Background(), tk, now))
	require.Equal(t, 2, f.updates, "SetResolved, plus SetFirstResponse backfilling the response (#219)")
	require.NotNil(t, f.record.ResolvedAt)
	require.True(t, f.record.ResolvedAt.Equal(now))
	require.NotNil(t, f.record.ResolutionElapsedAtMetSeconds)
	require.Equal(t, int64(3*time.Hour/time.Second), *f.record.ResolutionElapsedAtMetSeconds)
	require.NotNil(t, f.record.FirstResponseAt, "#219: a resolution with no prior reply satisfies the response target too")
	require.True(t, f.record.FirstResponseAt.Equal(now))

	// Re-resolving must not touch the store at all: the record is already
	// resolved, so RecordResolved is a full no-op (#220).
	later := now.Add(2 * time.Hour)
	require.NoError(t, sla.NewService(f).RecordResolved(context.Background(), tk, later))
	require.Equal(t, 2, f.updates, "already resolved: no further store writes")
	require.True(t, f.record.ResolvedAt.Equal(now), "already recorded")
	require.Equal(t, int64(3*time.Hour/time.Second), *f.record.ResolutionElapsedAtMetSeconds)
}

// TestRecordResolved_LateResolutionEntirelyBetweenSweepTicksStillStamped pins
// #217's root cause fix: before this, the ONLY place a late resolution got a
// breach stamp was the periodic sweep evaluating an outstanding record —
// nothing stamped it at the moment it was actually recorded, and
// ListSLABreachCandidates' resolved_at IS NULL guard means a ticket resolved
// late is never selected as a candidate again once resolved_at is set, losing
// the breach stamp forever. Simulated here by never calling
// EvaluateBreaches/SweepBreaches at all — RecordResolved alone must stamp it.
func TestRecordResolved_LateResolutionEntirelyBetweenSweepTicksStillStamped(t *testing.T) {
	store := newFakeSLAStore()
	policyID := uuid.New()
	ticketID := uuid.New()
	store.policies[policyID] = sla.Policy{ID: policyID, ResponseTargetMin: 30, ResolutionTargetMin: 60}
	store.records[ticketID] = sla.Record{TicketID: ticketID, PolicyID: policyID}

	createdAt := time.Now().Add(-2 * time.Hour)
	resolvedAt := createdAt.Add(90 * time.Minute) // 90 > 60-minute resolution target: late
	tk := ticket.Ticket{ID: ticketID, CreatedAt: createdAt}

	svc := sla.NewService(store)
	require.NoError(t, svc.RecordResolved(context.Background(), tk, resolvedAt))

	rec := store.records[ticketID]
	require.NotNil(t, rec.ResolutionBreachedAt, "a late resolution must be stamped at record time, not left for a sweep that never ran")
	require.True(t, rec.ResolutionBreachedAt.Equal(resolvedAt))
}

// TestRecordResolved_NoPriorReplyDoesNotShowFalseResponseBreach pins #219: a
// ticket resolved with no separate staff reply first must not read as a
// response breach purely because wall-clock time later passes the response
// target — the resolution itself satisfies the response target, at the
// resolution instant, and that reading is then frozen.
func TestRecordResolved_NoPriorReplyDoesNotShowFalseResponseBreach(t *testing.T) {
	store := newFakeSLAStore()
	policyID := uuid.New()
	ticketID := uuid.New()
	policy := sla.Policy{ID: policyID, ResponseTargetMin: 30, ResolutionTargetMin: 120}
	store.policies[policyID] = policy
	store.records[ticketID] = sla.Record{TicketID: ticketID, PolicyID: policyID}

	createdAt := time.Now().Add(-3 * time.Hour)
	// Resolved in 20 minutes: under the 30-minute response target AND the
	// 120-minute resolution target, with no separate reply ever recorded.
	resolvedAt := createdAt.Add(20 * time.Minute)
	tk := ticket.Ticket{ID: ticketID, CreatedAt: createdAt}

	svc := sla.NewService(store)
	require.NoError(t, svc.RecordResolved(context.Background(), tk, resolvedAt))

	rec := store.records[ticketID]
	require.NotNil(t, rec.FirstResponseAt, "the resolution must satisfy the response target")
	require.True(t, rec.FirstResponseAt.Equal(resolvedAt))
	require.Nil(t, rec.ResponseBreachedAt, "resolved on time toward the response target: not a breach")
	require.Nil(t, rec.ResolutionBreachedAt, "resolved on time toward the resolution target: not a breach")

	// The false-breach shape #219 fixes: checked much later, wall-clock time
	// has long since passed the 30-minute response target. Without the fix,
	// IsResponseBreached would recompute against `now` and read this as
	// breached; with FirstResponseAt now set, it must not.
	muchLater := resolvedAt.Add(10 * time.Hour)
	require.False(t, sla.IsResponseBreached(rec, policy, tk, muchLater),
		"a resolved ticket with no prior reply must never show a false response breach")
}

// TestRecordResolved_ClosedWithoutResolvingFreezesStatusInsteadOfGrowingLive
// pins #220: when close() (or the other UpdateStatus->Closed door) calls
// RecordResolved using closed_at as the resolution instant for a ticket that
// was never separately resolved, the SLA status must freeze at that instant
// — correctly red if it really was late — rather than keep computing a live,
// ever-growing Elapsed(t, now) against a ticket that will never move again.
func TestRecordResolved_ClosedWithoutResolvingFreezesStatusInsteadOfGrowingLive(t *testing.T) {
	store := newFakeSLAStore()
	policyID := uuid.New()
	ticketID := uuid.New()
	policy := sla.Policy{ID: policyID, Name: "Closed-without-resolving", ResponseTargetMin: 30, ResolutionTargetMin: 60}
	store.policies[policyID] = policy
	store.records[ticketID] = sla.Record{TicketID: ticketID, PolicyID: policyID}

	createdAt := time.Now().Add(-4 * time.Hour)
	closedAt := createdAt.Add(90 * time.Minute) // later than the 60-minute resolution target
	tk := ticket.Ticket{ID: ticketID, CreatedAt: createdAt}

	svc := sla.NewService(store)
	// This is exactly what ticket.Service.close()/UpdateStatus now do: call
	// RecordResolved with closed_at as the resolution instant.
	require.NoError(t, svc.RecordResolved(context.Background(), tk, closedAt))

	rec := store.records[ticketID]
	require.NotNil(t, rec.ResolvedAt, "close() must leave the record with a resolution recorded")
	require.NotNil(t, rec.ResolutionBreachedAt, "it really was late, so this must be a real, stamped breach")

	statusSoonAfter := sla.StatusFor(rec, policy, tk, closedAt.Add(time.Minute))
	statusMuchLater := sla.StatusFor(rec, policy, tk, closedAt.Add(24*30*time.Hour)) // a month later

	require.NotNil(t, statusSoonAfter.Resolution.MetAt, "frozen: the indicator must have a MetAt, not read as still outstanding")
	require.Equal(t, sla.Red, statusSoonAfter.Resolution.Color)
	require.Equal(t, statusSoonAfter.Resolution.ElapsedMin, statusMuchLater.Resolution.ElapsedMin,
		"a frozen reading must not keep growing the longer the closed ticket sits untouched")
	require.Equal(t, sla.Red, statusMuchLater.Resolution.Color)
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

// StatusesFor is the batch call the ticket list/detail handlers use (#183):
// two store calls however many tickets are passed, and a result keyed only by
// the tickets that actually have a record.
func TestSLAService_StatusesFor(t *testing.T) {
	t.Run("empty input makes zero store calls", func(t *testing.T) {
		store := newFakeSLAStore()
		svc := sla.NewService(store)

		got, err := svc.StatusesFor(context.Background(), nil, time.Now())
		require.NoError(t, err)
		require.Empty(t, got)
		require.Zero(t, store.listRecordsByIDsCalls)
		require.Zero(t, store.listPoliciesCalls)
	})

	t.Run("only tickets with a record appear, one records call and one policies call", func(t *testing.T) {
		store := newFakeSLAStore()
		policy := sla.Policy{ID: uuid.New(), Name: "Standard", ResponseTargetMin: 60, ResolutionTargetMin: 480}
		store.policies[policy.ID] = policy

		withRecordA := ticket.Ticket{ID: uuid.New(), CreatedAt: time.Now().Add(-10 * time.Minute)}
		withRecordB := ticket.Ticket{ID: uuid.New(), CreatedAt: time.Now().Add(-5 * time.Minute)}
		noRecordC := ticket.Ticket{ID: uuid.New(), CreatedAt: time.Now()}
		noRecordD := ticket.Ticket{ID: uuid.New(), CreatedAt: time.Now()}
		noRecordE := ticket.Ticket{ID: uuid.New(), CreatedAt: time.Now()}

		store.records[withRecordA.ID] = sla.Record{TicketID: withRecordA.ID, PolicyID: policy.ID}
		store.records[withRecordB.ID] = sla.Record{TicketID: withRecordB.ID, PolicyID: policy.ID}

		svc := sla.NewService(store)
		got, err := svc.StatusesFor(context.Background(),
			[]ticket.Ticket{withRecordA, withRecordB, noRecordC, noRecordD, noRecordE}, time.Now())
		require.NoError(t, err)

		require.Equal(t, 1, store.listRecordsByIDsCalls)
		require.Equal(t, 1, store.listPoliciesCalls)

		require.Len(t, got, 2)
		require.Contains(t, got, withRecordA.ID)
		require.Contains(t, got, withRecordB.ID)
		require.NotContains(t, got, noRecordC.ID)
		require.NotContains(t, got, noRecordD.ID)
		require.NotContains(t, got, noRecordE.ID)
		require.Equal(t, policy.ID, got[withRecordA.ID].PolicyID)
		require.Equal(t, policy.Name, got[withRecordA.ID].PolicyName)
	})

	t.Run("a record whose policy was deleted is skipped, not zero-valued", func(t *testing.T) {
		store := newFakeSLAStore()
		orphanID := uuid.New()
		tk := ticket.Ticket{ID: uuid.New(), CreatedAt: time.Now()}
		store.records[tk.ID] = sla.Record{TicketID: tk.ID, PolicyID: orphanID}

		svc := sla.NewService(store)
		got, err := svc.StatusesFor(context.Background(), []ticket.Ticket{tk}, time.Now())
		require.NoError(t, err)
		require.Empty(t, got)
	})
}

func prio(p ticket.Priority) *ticket.Priority { return &p }
