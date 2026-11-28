package sla_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// The sweep stamps what it finds on a ticket and raises at most one event for
// it, in one transaction (#670).

func TestEventNamesAreTheNotificationNames(t *testing.T) {
	require.Equal(t, notification.EventSLABreached, notification.EventType(sla.EventBreached))
	require.Equal(t, notification.EventSLAAtRisk, notification.EventType(sla.EventAtRisk))
}

// one ticket, one record, one policy, swept once.
type sweepCase struct {
	name         string
	resp, res    int           // the policy's targets in minutes
	age          time.Duration // the ticket's age at the sweep
	announce     bool
	preBreachedR bool // the response breach is already stamped

	wantEvent      notification.EventType // "" is none
	wantTargets    []string
	wantAtRisk     []string // nil: the key is absent
	wantSLA        *notification.SLAAlert
	wantStamped    [4]bool // response breach, resolution breach, response at risk, resolution at risk
	wantStale      int
	wantEvaluated  int
	wantStampedCnt int
}

func TestSweepBreaches_OneTransactionStampsAndRaisesOnePerTicket(t *testing.T) {
	min := time.Minute
	cases := []sweepCase{
		{
			name: "a fresh response breach", resp: 30, res: 120, age: 35 * min, announce: true,
			wantEvent: notification.EventSLABreached, wantTargets: []string{"response"},
			wantSLA:     &notification.SLAAlert{Breached: []notification.SLATarget{"response"}},
			wantStamped: [4]bool{true, false, false, false}, wantEvaluated: 1, wantStampedCnt: 1,
		},
		{
			// 80% of 30 is 24: a minute past it, nothing breached.
			name: "a fresh at-risk crossing alone", resp: 30, res: 120, age: 25 * min, announce: true,
			wantEvent: notification.EventSLAAtRisk, wantTargets: []string{"response"},
			wantSLA:     &notification.SLAAlert{AtRisk: []notification.SLATarget{"response"}},
			wantStamped: [4]bool{false, false, true, false}, wantEvaluated: 1, wantStampedCnt: 1,
		},
		{
			// Response breached a minute ago; 80% of 38 is 30.4, so resolution is
			// at risk since just before: one breach, one at-risk of the other
			// target, one event.
			name: "a breach and an at-risk of the other target are one breach event", resp: 30, res: 38,
			age: 31 * min, announce: true,
			wantEvent: notification.EventSLABreached, wantTargets: []string{"response"}, wantAtRisk: []string{"resolution"},
			wantSLA: &notification.SLAAlert{
				Breached: []notification.SLATarget{"response"}, AtRisk: []notification.SLATarget{"resolution"},
			},
			wantStamped: [4]bool{true, false, false, true}, wantEvaluated: 1, wantStampedCnt: 1,
		},
		{
			// Both targets of a one-minute policy are long past: stale, silent.
			name: "old breaches are stamped and nothing is sent", resp: 30, res: 120, age: 3 * time.Hour, announce: true,
			wantStamped: [4]bool{true, true, false, false}, wantStale: 2, wantEvaluated: 1, wantStampedCnt: 1,
		},
		{
			// Response (180) breached 5 minutes ago; resolution (200) crossed its
			// 80% line (160) 25 minutes ago. The at-risk stamp is set, the event
			// leaves it out.
			name: "a stale at-risk inside a fresh breach is stamped and left out", resp: 180, res: 200,
			age: 185 * min, announce: true,
			wantEvent: notification.EventSLABreached, wantTargets: []string{"response"},
			wantSLA:     &notification.SLAAlert{Breached: []notification.SLATarget{"response"}},
			wantStamped: [4]bool{true, false, false, true}, wantStale: 1, wantEvaluated: 1, wantStampedCnt: 1,
		},
		{
			// The mirror: a stale breach and a fresh at-risk of the other target.
			// Response (30) breached 16 minutes ago; resolution (50) crossed its
			// 80% line (40) 6 minutes ago.
			name: "a stale breach with a fresh at-risk raises the at-risk event", resp: 30, res: 50,
			age: 46 * min, announce: true,
			wantEvent: notification.EventSLAAtRisk, wantTargets: []string{"resolution"},
			wantSLA:     &notification.SLAAlert{AtRisk: []notification.SLATarget{"resolution"}},
			wantStamped: [4]bool{true, false, false, true}, wantStale: 1, wantEvaluated: 1, wantStampedCnt: 1,
		},
		{
			name: "the setting off stamps and sends nothing", resp: 30, res: 120, age: 35 * min, announce: false,
			wantStamped: [4]bool{true, false, false, false}, wantEvaluated: 1, wantStampedCnt: 1,
		},
		{
			name: "nothing due writes nothing at all", resp: 30, res: 120, age: 5 * min, announce: true,
			wantEvaluated: 1,
		},
		{
			// A breach stamped earlier never crosses again, so a policy loosened
			// in place since cannot turn it into an at-risk alert.
			name: "an already-breached target is not announced again", resp: 30, res: 120, age: 35 * min, announce: true,
			preBreachedR: true, wantStamped: [4]bool{true, false, false, false}, wantEvaluated: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSweepFx()
			now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
			policyID, ticketID := uuid.New(), uuid.New()
			f.slas.policies[policyID] = sla.Policy{ID: policyID, ResponseTargetMin: tc.resp, ResolutionTargetMin: tc.res}
			rec := sla.Record{ID: uuid.New(), TicketID: ticketID, PolicyID: policyID}
			if tc.preBreachedR {
				at := now.Add(-time.Hour)
				rec.ResponseBreachedAt = &at
			}
			f.slas.records[ticketID] = rec
			f.slas.candidates = []uuid.UUID{ticketID}
			f.tickets.tickets[ticketID] = ticket.Ticket{
				ID: ticketID, TrackingNumber: "GHD-2026-000007", Subject: "printer",
				CategoryID: uuid.New(), Priority: ticket.PriorityHigh, CreatedAt: now.Add(-tc.age),
			}

			svc := sla.NewService(f.slas, f.atomic)
			res, err := svc.SweepBreaches(context.Background(), f.tickets, now, tc.announce)
			require.NoError(t, err)

			wantAnnounced := 0
			if tc.wantEvent != "" {
				wantAnnounced = 1
			}
			require.Equal(t, sla.SweepResult{
				Evaluated: tc.wantEvaluated, Stamped: tc.wantStampedCnt, Announced: wantAnnounced, Stale: tc.wantStale,
			}, res)

			got := f.slas.records[ticketID]
			stamps := [4]bool{
				got.ResponseBreachedAt != nil, got.ResolutionBreachedAt != nil,
				got.ResponseAtRiskAt != nil, got.ResolutionAtRiskAt != nil,
			}
			require.Equal(t, tc.wantStamped, stamps)
			for _, ts := range []*time.Time{got.ResponseBreachedAt, got.ResolutionBreachedAt, got.ResponseAtRiskAt, got.ResolutionAtRiskAt} {
				if ts != nil && !tc.preBreachedR {
					require.True(t, ts.Equal(now), "stamped with the sweep's instant")
				}
			}

			if tc.wantEvent == "" {
				require.Empty(t, f.atomic.events)
				return
			}
			require.Len(t, f.atomic.events, 1, "at most one event per ticket per pass")
			ev := f.atomic.events[0]
			require.Equal(t, tc.wantEvent, ev.Type)
			require.Equal(t, []notification.EventType{tc.wantEvent}, ev.Kinds, "its own name only")
			require.Equal(t, ticketID, ev.TicketID)
			require.Nil(t, ev.ActorID, "the system acts")
			require.Equal(t, "GHD-2026-000007", ev.TrackingNumber)
			require.Equal(t, "printer", ev.Subject)
			require.True(t, ev.OccurredAt.Equal(now))
			require.NotNil(t, ev.Ticket, "the ticket as read in the transaction")
			require.Equal(t, "high", ev.Ticket.Priority)
			require.Equal(t, tc.wantSLA, ev.SLA)

			require.Equal(t, policyID, ev.Payload["policy_id"])
			require.Equal(t, f.slas.records[ticketID].ID, ev.Payload["record_id"])
			require.Equal(t, tc.wantTargets, ev.Payload["targets"])
			if tc.wantAtRisk == nil {
				require.NotContains(t, ev.Payload, "at_risk_targets", "absent, not empty")
			} else {
				require.Equal(t, tc.wantAtRisk, ev.Payload["at_risk_targets"])
			}
			require.Len(t, ev.Payload, map[bool]int{true: 4, false: 3}[tc.wantAtRisk != nil])
		})
	}
}

func TestSweepBreaches_NoTransactionWhenNothingIsDue(t *testing.T) {
	f := newSweepFx()
	now := time.Now()
	policyID, ticketID := uuid.New(), uuid.New()
	f.slas.policies[policyID] = sla.Policy{ID: policyID, ResponseTargetMin: 30, ResolutionTargetMin: 120}
	f.slas.records[ticketID] = sla.Record{TicketID: ticketID, PolicyID: policyID}
	f.slas.candidates = []uuid.UUID{ticketID}
	f.tickets.tickets[ticketID] = ticket.Ticket{ID: ticketID, CreatedAt: now.Add(-time.Minute)}

	_, err := sla.NewService(f.slas, f.atomic).SweepBreaches(context.Background(), f.tickets, now, true)
	require.NoError(t, err)
	require.Zero(t, f.atomic.txs)
	require.Zero(t, f.slas.stampCalls)
}

func TestSweepBreaches_ATicketWithNoRecordIsSkipped(t *testing.T) {
	f := newSweepFx()
	ticketID := uuid.New()
	f.slas.candidates = []uuid.UUID{ticketID}
	f.tickets.tickets[ticketID] = ticket.Ticket{ID: ticketID, CreatedAt: time.Now().Add(-time.Hour)}

	res, err := sla.NewService(f.slas, f.atomic).SweepBreaches(context.Background(), f.tickets, time.Now(), true)
	require.NoError(t, err)
	require.Equal(t, sla.SweepResult{Evaluated: 1}, res)
	require.Zero(t, f.atomic.txs)
}

// What the sweep decided from may be out of date by the time its statement runs.
// The statement says what it set; only that is announced.
func TestSweepBreaches_AnnouncesOnlyWhatTheStatementSet(t *testing.T) {
	setup := func() (*sweepFx, *sla.Service, uuid.UUID, time.Time) {
		f := newSweepFx()
		now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
		policyID, ticketID := uuid.New(), uuid.New()
		f.slas.policies[policyID] = sla.Policy{ID: policyID, ResponseTargetMin: 30, ResolutionTargetMin: 120}
		f.slas.records[ticketID] = sla.Record{TicketID: ticketID, PolicyID: policyID}
		f.slas.candidates = []uuid.UUID{ticketID}
		f.tickets.tickets[ticketID] = ticket.Ticket{ID: ticketID, CreatedAt: now.Add(-35 * time.Minute)}
		return f, sla.NewService(f.slas, f.atomic), ticketID, now
	}

	t.Run("a late reply that stamps first leaves nothing to announce", func(t *testing.T) {
		f, svc, ticketID, now := setup()
		f.store.beforeStamp = func() {
			r := f.slas.records[ticketID]
			reply := now.Add(-time.Minute)
			r.FirstResponseAt = &reply
			f.slas.records[ticketID] = r
		}
		res, err := svc.SweepBreaches(context.Background(), f.tickets, now, true)
		require.NoError(t, err)
		require.Empty(t, f.atomic.events)
		require.Equal(t, sla.SweepResult{Evaluated: 1}, res)
		require.Nil(t, f.slas.records[ticketID].ResponseBreachedAt)
	})

	t.Run("another sweep that stamped first leaves nothing to announce", func(t *testing.T) {
		f, svc, ticketID, now := setup()
		f.store.beforeStamp = func() {
			r := f.slas.records[ticketID]
			other := now.Add(-time.Second)
			r.ResponseBreachedAt = &other
			f.slas.records[ticketID] = r
		}
		res, err := svc.SweepBreaches(context.Background(), f.tickets, now, true)
		require.NoError(t, err)
		require.Empty(t, f.atomic.events)
		require.Equal(t, sla.SweepResult{Evaluated: 1}, res)
	})

	t.Run("a superseded record is skipped without error", func(t *testing.T) {
		f, svc, ticketID, now := setup()
		f.store.stampErr = ticket.ErrSLARecordSuperseded
		res, err := svc.SweepBreaches(context.Background(), f.tickets, now, true)
		require.NoError(t, err)
		require.Empty(t, f.atomic.events)
		require.Equal(t, sla.SweepResult{Evaluated: 1}, res)
		require.Nil(t, f.slas.records[ticketID].ResponseBreachedAt)
	})
}

func TestSweepBreaches_AFailedTransactionRaisesNothingAndTheOthersGoOn(t *testing.T) {
	f := newSweepFx()
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	policyID := uuid.New()
	f.slas.policies[policyID] = sla.Policy{ID: policyID, ResponseTargetMin: 30, ResolutionTargetMin: 120}
	badID, goodID := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{badID, goodID} {
		f.slas.records[id] = sla.Record{TicketID: id, PolicyID: policyID}
		f.tickets.tickets[id] = ticket.Ticket{ID: id, CreatedAt: now.Add(-35 * time.Minute)}
	}
	f.slas.candidates = []uuid.UUID{badID, goodID}

	boom := errors.New("read in the transaction failed")
	calls := 0
	f.store.beforeStamp = func() {
		calls++
		f.store.getErr = nil
		if calls == 1 {
			f.store.getErr = boom
		}
	}

	res, err := sla.NewService(f.slas, f.atomic).SweepBreaches(context.Background(), f.tickets, now, true)
	require.ErrorIs(t, err, boom)
	require.Equal(t, 1, res.Evaluated, "only the ticket that went through counts")
	require.Equal(t, 1, res.Announced)
	require.Len(t, f.atomic.events, 1)
	require.Equal(t, goodID, f.atomic.events[0].TicketID)
}

func TestSweepBreaches_WithoutATransactionRunnerItRefusesToStamp(t *testing.T) {
	f := newSweepFx()
	now := time.Now()
	policyID, ticketID := uuid.New(), uuid.New()
	f.slas.policies[policyID] = sla.Policy{ID: policyID, ResponseTargetMin: 30, ResolutionTargetMin: 120}
	f.slas.records[ticketID] = sla.Record{TicketID: ticketID, PolicyID: policyID}
	f.slas.candidates = []uuid.UUID{ticketID}
	f.tickets.tickets[ticketID] = ticket.Ticket{ID: ticketID, CreatedAt: now.Add(-time.Hour)}

	_, err := sla.NewService(f.slas, nil).SweepBreaches(context.Background(), f.tickets, now, true)
	require.Error(t, err)
	require.Nil(t, f.slas.records[ticketID].ResponseBreachedAt, "nothing was stamped outside a transaction")
}
