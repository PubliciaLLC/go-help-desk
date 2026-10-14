package ticketstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/slastore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// The four ticket.Store methods a priority change's SLA re-match runs on
// (#443b), against real Postgres.

type rematchStoreFx struct {
	ctx context.Context
	ts  *ticketstore.Store
	ss  *slastore.Store
	tk  ticket.Ticket
	cat category.Category
}

func newRematchStoreFx(t *testing.T) *rematchStoreFx {
	t.Helper()
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)
	q, rollback := testutil.TxQueries(t, db)
	t.Cleanup(rollback)
	ctx := context.Background()

	ts, ss := ticketstore.New(q), slastore.New(q)
	now := time.Now().UTC().Truncate(time.Millisecond)
	rep := user.User{
		ID: uuid.New(), Email: "rematch-store-" + uuid.NewString() + "@test.local", DisplayName: "Reporter",
		Role: user.RoleUser, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, userstore.New(q).Create(ctx, rep))
	cat := category.Category{ID: uuid.New(), Name: "RematchStore " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, categorystore.New(q).CreateCategory(ctx, cat))
	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)
	tk := ticket.Ticket{
		ID: uuid.New(), TrackingNumber: ticket.TrackingNumber("RS-" + uuid.NewString()[:8]),
		Subject: "rematch store", Description: "rematch store", CategoryID: cat.ID, Priority: ticket.PriorityMedium,
		StatusID: newSt.ID, ReporterUserID: &rep.ID, CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now,
	}
	require.NoError(t, ts.Create(ctx, tk))
	return &rematchStoreFx{ctx: ctx, ts: ts, ss: ss, tk: tk, cat: cat}
}

func (f *rematchStoreFx) policy(t *testing.T, name string, prio *ticket.Priority) sla.Policy {
	t.Helper()
	p := sla.Policy{ID: uuid.New(), Name: name, Priority: prio, ResponseTargetMin: 30, ResolutionTargetMin: 120}
	require.NoError(t, f.ss.CreatePolicy(f.ctx, p))
	return p
}

func TestCurrentSLARecordForUpdate(t *testing.T) {
	t.Run("a ticket with no record has none", func(t *testing.T) {
		f := newRematchStoreFx(t)
		got, err := f.ts.CurrentSLARecordForUpdate(f.ctx, f.tk.ID)
		require.NoError(t, err)
		require.Nil(t, got)
	})

	t.Run("a running record has no facts and carries its clock", func(t *testing.T) {
		f := newRematchStoreFx(t)
		p := f.policy(t, "running", nil)
		require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: f.tk.ID, PolicyID: p.ID}))

		got, err := f.ts.CurrentSLARecordForUpdate(f.ctx, f.tk.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Equal(t, p.ID, got.PolicyID)
		require.NotEqual(t, uuid.Nil, got.ID)
		require.Nil(t, got.StartedAt)
		require.Zero(t, got.PausedSecondsAtStart)
		require.Equal(t, ticket.SLAFacts{}, got.Facts, "nothing recorded yet")
	})

	// Every fact the record has recorded is read back, each on its own: a
	// re-match carries them onto the record that replaces it.
	at := time.Now().UTC().Truncate(time.Millisecond)
	settle := map[string]struct {
		set  func(f *rematchStoreFx) error
		want ticket.SLAFacts
	}{
		"first_response_at": {
			set:  func(f *rematchStoreFx) error { return f.ss.SetFirstResponse(f.ctx, f.tk.ID, at, 60, 1800) },
			want: ticket.SLAFacts{FirstResponseAt: &at, ResponseElapsedAtMetSeconds: ptr(int64(60))},
		},
		"resolved_at": {
			set:  func(f *rematchStoreFx) error { return f.ss.SetResolved(f.ctx, f.tk.ID, at, 90, 7200) },
			want: ticket.SLAFacts{ResolvedAt: &at, ResolutionElapsedAtMetSeconds: ptr(int64(90))},
		},
		"response_breached_at": {
			set:  func(f *rematchStoreFx) error { return f.ss.StampBreaches(f.ctx, f.tk.ID, &at, nil) },
			want: ticket.SLAFacts{ResponseBreachedAt: &at},
		},
		"resolution_breached_at": {
			set:  func(f *rematchStoreFx) error { return f.ss.StampBreaches(f.ctx, f.tk.ID, nil, &at) },
			want: ticket.SLAFacts{ResolutionBreachedAt: &at},
		},
	}
	for column, tc := range settle {
		t.Run("facts read back: "+column+" alone", func(t *testing.T) {
			f := newRematchStoreFx(t)
			p := f.policy(t, "facts", nil)
			require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: f.tk.ID, PolicyID: p.ID}))
			require.NoError(t, tc.set(f))

			got, err := f.ts.CurrentSLARecordForUpdate(f.ctx, f.tk.ID)
			require.NoError(t, err)
			require.NotNil(t, got)
			require.Equal(t, utcFacts(tc.want), utcFacts(got.Facts))
		})
	}
}

func ptr[T any](v T) *T { return &v }

// utcFacts puts every timestamp in UTC, so facts compare by instant rather than
// by the Location the driver happened to attach.
func utcFacts(f ticket.SLAFacts) ticket.SLAFacts {
	utc := func(t *time.Time) *time.Time {
		if t == nil {
			return nil
		}
		u := t.UTC()
		return &u
	}
	f.FirstResponseAt, f.ResolvedAt = utc(f.FirstResponseAt), utc(f.ResolvedAt)
	f.ResponseBreachedAt, f.ResolutionBreachedAt = utc(f.ResponseBreachedAt), utc(f.ResolutionBreachedAt)
	return f
}

// A second current row is refused until the first is superseded: the schema
// is the guard behind the ticket row lock.
func TestSLARematchStore_StartRefusesASecondCurrentRecord(t *testing.T) {
	f := newRematchStoreFx(t)
	p := f.policy(t, "p", nil)
	require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: f.tk.ID, PolicyID: p.ID}))

	started := time.Now().UTC().Truncate(time.Millisecond)
	err := f.ts.StartSLARecord(f.ctx, ticket.SLARecordStart{
		ID: uuid.New(), TicketID: f.tk.ID, PolicyID: p.ID, StartedAt: &started,
	})
	require.Error(t, err)
}

func TestSLARematchStore_SupersedeAndStartRoundTrip(t *testing.T) {
	f := newRematchStoreFx(t)
	old := f.policy(t, "old", nil)
	next := f.policy(t, "next", nil)
	require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: f.tk.ID, PolicyID: old.ID}))
	require.NoError(t, f.ss.SetFirstResponse(f.ctx, f.tk.ID, time.Now().UTC().Truncate(time.Millisecond), 60, 1800))
	before, err := f.ss.GetRecord(f.ctx, f.tk.ID)
	require.NoError(t, err)

	cur, err := f.ts.CurrentSLARecordForUpdate(f.ctx, f.tk.ID)
	require.NoError(t, err)
	at := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, f.ts.SupersedeSLARecord(f.ctx, cur.ID, at))

	started := at
	startNew := ticket.SLARecordStart{
		ID: uuid.New(), TicketID: f.tk.ID, PolicyID: next.ID, StartedAt: &started, PausedSecondsAtStart: 90,
		Facts: cur.Facts, // a staff re-match carries the facts onto the new record
	}
	require.NoError(t, f.ts.StartSLARecord(f.ctx, startNew))

	now, err := f.ss.GetRecord(f.ctx, f.tk.ID)
	require.NoError(t, err)
	require.Equal(t, startNew.ID, now.ID)
	require.Equal(t, next.ID, now.PolicyID)
	require.NotNil(t, now.StartedAt)
	require.True(t, now.StartedAt.Equal(started))
	require.EqualValues(t, 90, now.PausedSecondsAtStart)
	require.NotNil(t, now.FirstResponseAt, "the facts were carried onto the new record")
	require.True(t, now.FirstResponseAt.Equal(*cur.Facts.FirstResponseAt))
	require.Equal(t, cur.Facts.ResponseElapsedAtMetSeconds, now.ResponseElapsedAtMetSeconds)

	// The new record is the one in force, and the old one is gone from view.
	again, err := f.ts.CurrentSLARecordForUpdate(f.ctx, f.tk.ID)
	require.NoError(t, err)
	require.Equal(t, startNew.ID, again.ID)

	// Superseding the old row again matches nothing: it is no longer current.
	require.Error(t, f.ts.SupersedeSLARecord(f.ctx, cur.ID, at))

	// The old row kept every fact it had.
	require.Equal(t, old.ID, before.PolicyID)
	require.NotNil(t, before.FirstResponseAt)
}

func TestMatchSLAPolicy(t *testing.T) {
	f := newRematchStoreFx(t)

	got, err := f.ts.MatchSLAPolicy(f.ctx, ticket.PriorityHigh, f.cat.ID)
	require.NoError(t, err)
	require.Nil(t, got, "no policy configured")

	high := ticket.PriorityHigh
	p := f.policy(t, "high only", &high)
	catchAll := f.policy(t, "catch-all", nil)

	got, err = f.ts.MatchSLAPolicy(f.ctx, ticket.PriorityHigh, f.cat.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, p.ID, *got, "the more specific tier wins")

	got, err = f.ts.MatchSLAPolicy(f.ctx, ticket.PriorityLow, f.cat.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, catchAll.ID, *got, "falls through to the catch-all")
}
