package database_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/txrunner"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// The SLA sweep stamps and raises in one transaction (#670), against real
// Postgres with committed rows: the properties are about commit and rollback,
// the row lock, and what lands in the outbox. Each fixture has a database of its
// own, so the outbox is its alone.

var sweepChannels = []string{"email", "webhook"}

type sweepPG struct {
	*changePriorityFx
	svc *sla.Service
	now time.Time
}

func newSweepPG(t *testing.T) *sweepPG {
	t.Helper()
	f := newChangePriorityFx(t)
	return &sweepPG{
		changePriorityFx: f,
		svc:              sla.NewService(f.ss, txrunner.New(f.db.SQL, sweepChannels, nil)),
		now:              time.Now().UTC().Truncate(time.Millisecond),
	}
}

func (f *sweepPG) policy(t *testing.T, respMin, resMin int) sla.Policy {
	t.Helper()
	p := sla.Policy{ID: uuid.New(), Name: "sweep " + uuid.NewString()[:8], ResponseTargetMin: respMin, ResolutionTargetMin: resMin}
	require.NoError(t, f.ss.CreatePolicy(f.ctx, p))
	return p
}

// ticket plants an open ticket created age before f.now with a current record
// under p.
func (f *sweepPG) ticket(t *testing.T, age time.Duration, p sla.Policy) (ticket.Ticket, sla.Record) {
	t.Helper()
	tk := f.tk
	tk.ID = uuid.New()
	tk.TrackingNumber = ticket.TrackingNumber("SW-" + uuid.NewString()[:8])
	tk.CreatedAt = f.now.Add(-age)
	tk.UpdatedAt = tk.CreatedAt
	require.NoError(t, f.ts.Create(f.ctx, tk))
	require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: tk.ID, PolicyID: p.ID}))
	rec, err := f.ss.GetRecord(f.ctx, tk.ID)
	require.NoError(t, err)
	return tk, rec
}

func (f *sweepPG) sweep(t *testing.T, at time.Time, announce bool) sla.SweepResult {
	t.Helper()
	res, err := f.svc.SweepBreaches(f.ctx, f.ts, at, announce)
	require.NoError(t, err)
	return res
}

// outboxRow is one outbox row, decoded.
type outboxRow struct {
	channel string
	ev      notification.Event
	raw     map[string]any // the stored JSON of its payload
}

// outbox returns the committed rows for a ticket, oldest first.
func (f *sweepPG) outbox(t *testing.T, id uuid.UUID) []outboxRow {
	t.Helper()
	rows, err := f.db.SQL.QueryContext(f.ctx,
		`SELECT channel, event FROM notification_outbox WHERE event->>'ticket_id' = $1 ORDER BY created_at, id`, id.String())
	require.NoError(t, err)
	defer rows.Close()
	var out []outboxRow
	for rows.Next() {
		var ch string
		var body []byte
		require.NoError(t, rows.Scan(&ch, &body))
		ev, err := notification.DecodeEvent(body)
		require.NoError(t, err)
		var stored struct {
			Payload map[string]any `json:"payload"`
		}
		require.NoError(t, json.Unmarshal(body, &stored))
		out = append(out, outboxRow{channel: ch, ev: ev, raw: stored.Payload})
	}
	require.NoError(t, rows.Err())
	return out
}

func (f *sweepPG) outboxTotal(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, f.db.SQL.QueryRowContext(f.ctx, `SELECT count(*) FROM notification_outbox`).Scan(&n))
	return n
}

// perChannel counts a ticket's rows by channel.
func (f *sweepPG) perChannel(t *testing.T, id uuid.UUID) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, q := range f.outbox(t, id) {
		out[q.channel]++
	}
	return out
}

func (f *sweepPG) row(t *testing.T, ticketID uuid.UUID) dbgen.SlaRecord {
	t.Helper()
	r, err := f.q.GetSLARecord(f.ctx, ticketID)
	require.NoError(t, err)
	return r
}

type stampSet struct{ respBreach, resBreach, respRisk, resRisk bool }

func stampsOf(r dbgen.SlaRecord) stampSet {
	return stampSet{r.ResponseBreachedAt.Valid, r.ResolutionBreachedAt.Valid, r.ResponseAtRiskAt.Valid, r.ResolutionAtRiskAt.Valid}
}

// the email row of a ticket's only event, checked against its webhook twin.
func (f *sweepPG) onlyEvent(t *testing.T, id uuid.UUID) outboxRow {
	t.Helper()
	rows := f.outbox(t, id)
	require.Len(t, rows, len(sweepChannels), "one row per channel")
	require.Equal(t, rows[0].ev.Type, rows[1].ev.Type)
	require.Equal(t, rows[0].raw, rows[1].raw)
	return rows[0]
}

func strs(v any) []string {
	items, _ := v.([]any)
	out := make([]string, len(items))
	for i, it := range items {
		out[i], _ = it.(string)
	}
	return out
}

func TestSweepAlerts_OneEventAndOneRowPerChannelPerTicketPerPass(t *testing.T) {
	f := newSweepPG(t)
	p := f.policy(t, 30, 120)
	tk, rec := f.ticket(t, 35*time.Minute, p)

	res := f.sweep(t, f.now, true)
	require.Equal(t, sla.SweepResult{Evaluated: 1, Stamped: 1, Announced: 1}, res)

	require.Equal(t, map[string]int{"email": 1, "webhook": 1}, f.perChannel(t, tk.ID))
	q := f.onlyEvent(t, tk.ID)
	require.Equal(t, notification.EventSLABreached, q.ev.Type)
	require.Equal(t, []notification.EventType{notification.EventSLABreached}, q.ev.Kinds)
	require.Nil(t, q.ev.ActorID)
	require.Equal(t, string(tk.TrackingNumber), q.ev.TrackingNumber)
	require.Equal(t, tk.Subject, q.ev.Subject)
	require.NotNil(t, q.ev.Ticket, "the ticket as read in the transaction")
	require.Equal(t, tk.CategoryID, q.ev.Ticket.CategoryID)
	require.Equal(t, &notification.SLAAlert{Breached: []notification.SLATarget{notification.SLATargetResponse}}, q.ev.SLA)
	require.Equal(t, []string{"response"}, strs(q.raw["targets"]))
	require.Equal(t, p.ID.String(), q.raw["policy_id"])
	require.Equal(t, rec.ID.String(), q.raw["record_id"])
	require.NotContains(t, q.raw, "at_risk_targets", "absent, not empty")
	require.Len(t, q.raw, 3, "ids and enums only")
	require.Equal(t, stampSet{respBreach: true}, stampsOf(f.row(t, tk.ID)))

	// The stamp is the fact the next pass reads: nothing is announced twice.
	require.Equal(t, sla.SweepResult{}, f.sweep(t, f.now.Add(time.Minute), true), "no candidates left")
	require.Equal(t, map[string]int{"email": 1, "webhook": 1}, f.perChannel(t, tk.ID))
}

func TestSweepAlerts_AFreshAtRiskAlone(t *testing.T) {
	f := newSweepPG(t)
	p := f.policy(t, 30, 120)
	tk, _ := f.ticket(t, 25*time.Minute, p) // 80% of 30 is 24

	res := f.sweep(t, f.now, true)
	require.Equal(t, sla.SweepResult{Evaluated: 1, Stamped: 1, Announced: 1}, res)

	q := f.onlyEvent(t, tk.ID)
	require.Equal(t, notification.EventSLAAtRisk, q.ev.Type)
	require.Equal(t, []string{"response"}, strs(q.raw["targets"]))
	require.NotContains(t, q.raw, "at_risk_targets", "not a key of this event")
	require.Equal(t, &notification.SLAAlert{AtRisk: []notification.SLATarget{notification.SLATargetResponse}}, q.ev.SLA)
	require.Equal(t, stampSet{respRisk: true}, stampsOf(f.row(t, tk.ID)))
}

func TestSweepAlerts_BreachAndAtRiskOnDifferentTargetsAreOneBreachEvent(t *testing.T) {
	f := newSweepPG(t)
	p := f.policy(t, 30, 38) // at 31 minutes: response breached, resolution past its 30.4 line
	tk, _ := f.ticket(t, 31*time.Minute, p)

	res := f.sweep(t, f.now, true)
	require.Equal(t, sla.SweepResult{Evaluated: 1, Stamped: 1, Announced: 1}, res)

	require.Equal(t, map[string]int{"email": 1, "webhook": 1}, f.perChannel(t, tk.ID), "one event, not two")
	q := f.onlyEvent(t, tk.ID)
	require.Equal(t, notification.EventSLABreached, q.ev.Type)
	require.Equal(t, []string{"response"}, strs(q.raw["targets"]))
	require.Equal(t, []string{"resolution"}, strs(q.raw["at_risk_targets"]))
	require.Equal(t, &notification.SLAAlert{
		Breached: []notification.SLATarget{notification.SLATargetResponse},
		AtRisk:   []notification.SLATarget{notification.SLATargetResolution},
	}, q.ev.SLA)
	require.Equal(t, stampSet{respBreach: true, resRisk: true}, stampsOf(f.row(t, tk.ID)), "both stamps set")
}

func TestSweepAlerts_BothTargetsInOneEvent(t *testing.T) {
	f := newSweepPG(t)
	p := f.policy(t, 30, 30)
	tk, _ := f.ticket(t, 30*time.Minute, p)

	f.sweep(t, f.now, true)

	q := f.onlyEvent(t, tk.ID)
	require.Equal(t, notification.EventSLABreached, q.ev.Type)
	require.Equal(t, []string{"response", "resolution"}, strs(q.raw["targets"]))
	require.Equal(t, stampSet{respBreach: true, resBreach: true}, stampsOf(f.row(t, tk.ID)))
}

// Freshness is per crossing, inside one notification too: a stale at-risk next
// to a fresh breach is stamped and left out of the event, and the other way
// round the at-risk event goes out alone.
func TestSweepAlerts_FreshAndStaleAreDecidedPerCrossing(t *testing.T) {
	cases := []struct {
		name         string
		resp, res    int
		age          time.Duration
		wantType     notification.EventType
		wantTargets  []string
		wantAtRisk   []string // nil: the key is absent
		wantStamps   stampSet
		wantStaleCnt int
	}{
		{
			name: "fresh at-risk rides on the breach", resp: 30, res: 38, age: 31 * time.Minute,
			wantType: notification.EventSLABreached, wantTargets: []string{"response"}, wantAtRisk: []string{"resolution"},
			wantStamps: stampSet{respBreach: true, resRisk: true},
		},
		{
			// Response (180) breached 5 minutes ago; resolution (200) crossed its
			// 160-minute line 25 minutes ago.
			name: "stale at-risk is stamped and left out of a fresh breach", resp: 180, res: 200, age: 185 * time.Minute,
			wantType: notification.EventSLABreached, wantTargets: []string{"response"},
			wantStamps: stampSet{respBreach: true, resRisk: true}, wantStaleCnt: 1,
		},
		{
			// Response (30) breached 16 minutes ago; resolution (50) crossed 40
			// six minutes ago.
			name: "stale breach, fresh at-risk: the at-risk event", resp: 30, res: 50, age: 46 * time.Minute,
			wantType: notification.EventSLAAtRisk, wantTargets: []string{"resolution"},
			wantStamps: stampSet{respBreach: true, resRisk: true}, wantStaleCnt: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSweepPG(t)
			tk, _ := f.ticket(t, tc.age, f.policy(t, tc.resp, tc.res))

			res := f.sweep(t, f.now, true)
			require.Equal(t, sla.SweepResult{Evaluated: 1, Stamped: 1, Announced: 1, Stale: tc.wantStaleCnt}, res)

			q := f.onlyEvent(t, tk.ID)
			require.Equal(t, tc.wantType, q.ev.Type)
			require.Equal(t, tc.wantTargets, strs(q.raw["targets"]))
			if tc.wantAtRisk == nil {
				require.NotContains(t, q.raw, "at_risk_targets", "key absent")
			} else {
				require.Equal(t, tc.wantAtRisk, strs(q.raw["at_risk_targets"]))
			}
			require.Equal(t, tc.wantStamps, stampsOf(f.row(t, tk.ID)), "the stamp is set either way")
		})
	}
}

// A target announced at risk with a breach is never announced at risk again;
// when it breaches, a later pass says so as for any breach.
func TestSweepAlerts_AtRiskTargetThatBreachesLaterIsABreachOfThatTargetOnly(t *testing.T) {
	f := newSweepPG(t)
	p := f.policy(t, 30, 38)
	tk, _ := f.ticket(t, 31*time.Minute, p)

	f.sweep(t, f.now, true)
	require.Len(t, f.outbox(t, tk.ID), 2)

	later := f.now.Add(8 * time.Minute) // 39 minutes: resolution is past 38
	res := f.sweep(t, later, true)
	require.Equal(t, sla.SweepResult{Evaluated: 1, Stamped: 1, Announced: 1}, res)

	rows := f.outbox(t, tk.ID)
	require.Len(t, rows, 4, "two events, two channels")
	second := rows[2]
	require.Equal(t, notification.EventSLABreached, second.ev.Type)
	require.Equal(t, []string{"resolution"}, strs(second.raw["targets"]))
	require.NotContains(t, second.raw, "at_risk_targets")
	require.Equal(t, stampSet{respBreach: true, resBreach: true, resRisk: true}, stampsOf(f.row(t, tk.ID)))
}

// Every replica runs a sweep: two decide the same crossing, the row lock puts
// one after the other, and the second finds the column set and announces nothing.
func TestSweepAlerts_ConcurrentSweepsGiveOneEvent(t *testing.T) {
	f := newSweepPG(t)
	p := f.policy(t, 30, 38)
	var tickets []ticket.Ticket
	for range 8 {
		tk, _ := f.ticket(t, 31*time.Minute, p)
		tickets = append(tickets, tk)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]sla.SweepResult, 3)
	errs := make([]error, 3)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i], errs[i] = f.svc.SweepBreaches(f.ctx, f.ts, f.now, true)
		}()
	}
	close(start)
	wg.Wait()

	announced := 0
	for i := range results {
		require.NoError(t, errs[i])
		announced += results[i].Announced
	}
	require.Equal(t, len(tickets), announced, "each ticket announced by exactly one of the sweeps")
	for _, tk := range tickets {
		require.Equal(t, map[string]int{"email": 1, "webhook": 1}, f.perChannel(t, tk.ID), "one event")
		require.Equal(t, stampSet{respBreach: true, resRisk: true}, stampsOf(f.row(t, tk.ID)))
	}
}

// beforeTx runs once, just before the first transaction begins: what another
// writer did after the sweep read the record and before its statement.
type beforeTx struct {
	inner  ticket.Atomic
	before func()
	once   sync.Once
}

func (a *beforeTx) InTx(ctx context.Context, fn func(ticket.Store, audit.Store, *notification.Changes) error) error {
	a.once.Do(a.before)
	return a.inner.InTx(ctx, fn)
}

// A late reply that stamps first (RecordFirstResponse is the same statement the
// reply's own commit follows) sends nothing: the target is met.
func TestSweepAlerts_ALateReplyThatStampsFirstAnnouncesNothing(t *testing.T) {
	f := newSweepPG(t)
	p := f.policy(t, 30, 120)
	tk, rec := f.ticket(t, 35*time.Minute, p)

	reply := func() {
		// The reply lands at 34 minutes: already late, so the same statement
		// that records it stamps the breach.
		require.NoError(t, f.ss.SetFirstResponse(f.ctx, rec.ID, f.now.Add(-time.Minute), int64(34*60), int64(30*60)))
	}
	svc := sla.NewService(f.ss, &beforeTx{inner: txrunner.New(f.db.SQL, sweepChannels, nil), before: reply})

	res, err := svc.SweepBreaches(f.ctx, f.ts, f.now, true)
	require.NoError(t, err)
	require.Equal(t, sla.SweepResult{Evaluated: 1}, res)

	require.Empty(t, f.outbox(t, tk.ID), "the reply is a person acting; the sweep says nothing")
	row := f.row(t, tk.ID)
	require.True(t, row.ResponseBreachedAt.Valid, "the reply's own statement stamped it")
	require.True(t, row.FirstResponseAt.Valid)
}

// The other order: the sweep committed first, so the breach was true at its
// reading and is announced; the reply then records its response and the stamp stays.
func TestSweepAlerts_ASweepThatStampsFirstStillAnnouncesAndTheReplyKeepsTheStamp(t *testing.T) {
	f := newSweepPG(t)
	p := f.policy(t, 30, 120)
	tk, rec := f.ticket(t, 35*time.Minute, p)

	f.sweep(t, f.now, true)
	stamp := f.row(t, tk.ID).ResponseBreachedAt
	require.NoError(t, f.ss.SetFirstResponse(f.ctx, rec.ID, f.now.Add(time.Minute), int64(36*60), int64(30*60)))

	require.Equal(t, map[string]int{"email": 1, "webhook": 1}, f.perChannel(t, tk.ID))
	row := f.row(t, tk.ID)
	require.True(t, row.FirstResponseAt.Valid)
	require.True(t, stamp.Time.Equal(row.ResponseBreachedAt.Time), "the reply keeps the stamp")
}

// A ticket whose record is attached with a clock already past its targets
// (a policy attached to an old ticket, or a clock resumed on an old one) is
// stamped silently.
func TestSweepAlerts_AttachedWithAnAlreadyOverdueClockIsStampedSilently(t *testing.T) {
	f := newSweepPG(t)
	p := f.policy(t, 30, 120)
	tk, _ := f.ticket(t, 5*time.Hour, p)

	res := f.sweep(t, f.now, true)
	require.Equal(t, sla.SweepResult{Evaluated: 1, Stamped: 1, Stale: 2}, res)
	require.Empty(t, f.outbox(t, tk.ID))
	require.Equal(t, stampSet{respBreach: true, resBreach: true}, stampsOf(f.row(t, tk.ID)))
}

// A tightened policy, a downtime or SLA switched back on can put a whole queue
// past its targets at once: all stamped, nothing queued.
func TestSweepAlerts_ABacklogOfOverdueTicketsLeavesNoOutboxRow(t *testing.T) {
	f := newSweepPG(t)
	p := f.policy(t, 30, 120)
	const n = 1000
	ids := make([]uuid.UUID, 0, n)
	for range n {
		tk, _ := f.ticket(t, 3*time.Hour, p)
		ids = append(ids, tk.ID)
	}

	res := f.sweep(t, f.now, true)
	require.Equal(t, sla.SweepResult{Evaluated: n, Stamped: n, Stale: 2 * n}, res)
	require.Zero(t, f.outboxTotal(t), "a backlog sends nothing")

	var stamped int
	require.NoError(t, f.db.SQL.QueryRowContext(f.ctx,
		`SELECT count(*) FROM sla_records WHERE response_breached_at IS NOT NULL AND resolution_breached_at IS NOT NULL`).Scan(&stamped))
	require.Equal(t, n, stamped, "every one is stamped")
	require.Equal(t, sla.SweepResult{}, f.sweep(t, f.now.Add(time.Minute), true), "and none is a candidate again")
	_ = ids
}

// failAfterFn runs the transaction's whole function, stamp and raise included,
// and then fails, so what rolls back is a complete piece of work.
type failAfterFn struct {
	inner ticket.Atomic
	err   error
}

func (a failAfterFn) InTx(ctx context.Context, fn func(ticket.Store, audit.Store, *notification.Changes) error) error {
	return a.inner.InTx(ctx, func(st ticket.Store, au audit.Store, ch *notification.Changes) error {
		if err := fn(st, au, ch); err != nil {
			return err
		}
		return a.err
	})
}

func TestSweepAlerts_ARolledBackTransactionLeavesNoStampAndNoRow(t *testing.T) {
	f := newSweepPG(t)
	p := f.policy(t, 30, 38)
	tk, _ := f.ticket(t, 31*time.Minute, p)

	boom := errors.New("the commit never happened")
	failing := sla.NewService(f.ss, failAfterFn{inner: txrunner.New(f.db.SQL, sweepChannels, nil), err: boom})
	res, err := failing.SweepBreaches(f.ctx, f.ts, f.now, true)
	require.ErrorIs(t, err, boom)
	require.Equal(t, sla.SweepResult{}, res)

	require.Equal(t, stampSet{}, stampsOf(f.row(t, tk.ID)), "no stamp")
	require.Empty(t, f.outbox(t, tk.ID), "no row")

	// The next pass finds the record unstamped and announces what is still fresh.
	res = f.sweep(t, f.now, true)
	require.Equal(t, sla.SweepResult{Evaluated: 1, Stamped: 1, Announced: 1}, res)
	require.Equal(t, map[string]int{"email": 1, "webhook": 1}, f.perChannel(t, tk.ID))
}

func TestSweepAlerts_WithTheSettingOffItStampsAndSendsNothing(t *testing.T) {
	f := newSweepPG(t)
	p := f.policy(t, 30, 38)
	tk, _ := f.ticket(t, 31*time.Minute, p)

	res := f.sweep(t, f.now, false)
	require.Equal(t, sla.SweepResult{Evaluated: 1, Stamped: 1}, res)
	require.Empty(t, f.outbox(t, tk.ID))
	require.Equal(t, stampSet{respBreach: true, resRisk: true}, stampsOf(f.row(t, tk.ID)), "breach and at-risk alike are stamped")

	// Switched back on, it does not announce what it stamped while off.
	require.Equal(t, sla.SweepResult{}, f.sweep(t, f.now.Add(time.Minute), true))
	require.Empty(t, f.outbox(t, tk.ID), "a crossing stamped while it is off is never announced later")
}

// A record a re-match superseded is skipped without error and without a row:
// the next pass reads what replaced it.
func TestSweepAlerts_ASupersededRecordIsNeitherStampedNorAnnounced(t *testing.T) {
	f := newSweepPG(t)
	p := f.policy(t, 30, 120)
	tk, rec := f.ticket(t, 35*time.Minute, p)

	supersede := func() {
		n, err := f.q.SupersedeSLARecord(f.ctx, dbgen.SupersedeSLARecordParams{ID: rec.ID, SupersededAt: f.now})
		require.NoError(t, err)
		require.EqualValues(t, 1, n)
	}
	svc := sla.NewService(f.ss, &beforeTx{inner: txrunner.New(f.db.SQL, sweepChannels, nil), before: supersede})

	res, err := svc.SweepBreaches(f.ctx, f.ts, f.now, true)
	require.NoError(t, err)
	require.Equal(t, sla.SweepResult{Evaluated: 1}, res)
	require.Empty(t, f.outbox(t, tk.ID))
	all, current := f.recordsOf(t, tk.ID)
	require.Len(t, all, 1)
	require.Empty(t, current)
	require.Equal(t, stampSet{}, stampsOf(all[0]))
}
