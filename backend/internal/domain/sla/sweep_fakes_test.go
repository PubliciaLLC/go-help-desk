package sla_test

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// The sweep stamps and raises through ticket.Atomic (#670). These are its fakes:
// a ticket.Store that answers only the two calls the sweep makes, over the same
// in-memory records the rest of this package's tests read, and an Atomic that
// keeps the events a transaction raised only when it succeeds.

// fakeTicketStore embeds the interface and overrides only what the sweep uses,
// so a call it should not make is a nil dereference, not a silent zero.
type fakeTicketStore struct {
	ticket.Store
	slas    *fakeSLAStore
	tickets *fakeTickets

	// stampErr is returned by StampSLACrossings, after beforeStamp has run.
	stampErr error
	// beforeStamp runs at the start of StampSLACrossings: what another writer
	// did between the sweep's reading and its statement.
	beforeStamp func()
	// getErr is returned by GetByID, the read inside the transaction.
	getErr error
}

func (f *fakeTicketStore) StampSLACrossings(_ context.Context, recordID uuid.UUID, s ticket.SLAStampRequest) (ticket.SLAStamped, error) {
	f.slas.stampCalls++
	if f.beforeStamp != nil {
		f.beforeStamp()
	}
	if f.stampErr != nil {
		return ticket.SLAStamped{}, f.stampErr
	}
	r, err := f.slas.find(recordID)
	if err != nil {
		return ticket.SLAStamped{}, ticket.ErrSLARecordSuperseded
	}
	var got ticket.SLAStamped
	at := s.At
	set := func(col **time.Time, want, ok bool) bool {
		if !want || !ok || *col != nil {
			return false
		}
		*col = &at
		return true
	}
	respOpen := r.FirstResponseAt == nil && r.ResponseBreachedAt == nil
	resOpen := r.ResolvedAt == nil && r.ResolutionBreachedAt == nil
	got.ResponseAtRisk = set(&r.ResponseAtRiskAt, s.ResponseAtRisk, respOpen && !s.ResponseBreach)
	got.ResolutionAtRisk = set(&r.ResolutionAtRiskAt, s.ResolutionAtRisk, resOpen && !s.ResolutionBreach)
	got.ResponseBreach = set(&r.ResponseBreachedAt, s.ResponseBreach, r.FirstResponseAt == nil)
	got.ResolutionBreach = set(&r.ResolutionBreachedAt, s.ResolutionBreach, r.ResolvedAt == nil)
	f.slas.records[r.TicketID] = r
	return got, nil
}

func (f *fakeTicketStore) GetByID(ctx context.Context, id uuid.UUID) (ticket.Ticket, error) {
	if f.getErr != nil {
		return ticket.Ticket{}, f.getErr
	}
	return f.tickets.GetByID(ctx, id)
}

// fakeAtomic runs fn against one store. Events raised in a transaction that
// returned an error are dropped, as the production runner's rollback drops the
// outbox rows.
type fakeAtomic struct {
	store  ticket.Store
	txs    int
	events []notification.Event
}

func (a *fakeAtomic) InTx(_ context.Context, fn func(ticket.Store, audit.Store, *notification.Changes) error) error {
	a.txs++
	ch := &notification.Changes{}
	if err := fn(a.store, nil, ch); err != nil {
		return err
	}
	a.events = append(a.events, ch.Events()...)
	return nil
}

// sweepAtomic is the Atomic the older sweep tests need: the store and the
// tickets they already build, wired as a transaction.
func sweepAtomic(slas *fakeSLAStore, tickets *fakeTickets) *fakeAtomic {
	return &fakeAtomic{store: &fakeTicketStore{slas: slas, tickets: tickets}}
}

// newSweepFx wires a fake SLA store, tickets, a ticket store over them and an
// Atomic over that.
type sweepFx struct {
	slas    *fakeSLAStore
	tickets *fakeTickets
	store   *fakeTicketStore
	atomic  *fakeAtomic
}

func newSweepFx() *sweepFx {
	f := &sweepFx{slas: newFakeSLAStore(), tickets: newFakeTickets()}
	f.store = &fakeTicketStore{slas: f.slas, tickets: f.tickets}
	f.atomic = &fakeAtomic{store: f.store}
	return f
}
