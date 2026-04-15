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
