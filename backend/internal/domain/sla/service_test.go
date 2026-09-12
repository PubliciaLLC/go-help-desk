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
	f.records[r.TicketID] = r
	return nil
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
		Priority:            ticket.PriorityMedium,
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

	onTime := deadline.Add(-10 * time.Minute)
	late := deadline.Add(10 * time.Minute)
	now := time.Now()

	require.False(t,
		sla.IsResolutionBreached(sla.Record{ResolvedAt: &onTime}, policy, created, now),
		"resolved before the deadline is not a breach, however long ago that was")

	require.True(t,
		sla.IsResolutionBreached(sla.Record{ResolvedAt: &late}, policy, created, now),
		"resolved after the deadline is a breach")

	require.True(t,
		sla.IsResolutionBreached(sla.Record{}, policy, created, now),
		"unresolved past the deadline is a breach")
}
