package database_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// StampSLACrossings against real Postgres (#670). It is the one statement the
// sweep stamps with, and it has to say which columns IT set, because only a
// column this statement set may be announced.

// stampFx is a ticket with a current record, ready to be stamped.
type stampFx struct {
	*rematchFx
	tk  ticket.Ticket
	rec sla.Record
	at  time.Time
}

func newStampFx(t *testing.T) *stampFx {
	t.Helper()
	f := newRematchFx(t)
	tk := f.ticket(t, 3*time.Hour, nil, 0)
	p := f.policy(t, "stamp", 30, 120)
	require.NoError(t, f.ss.CreateRecord(f.ctx, sla.Record{TicketID: tk.ID, PolicyID: p.ID}))
	rec, err := f.ss.GetRecord(f.ctx, tk.ID)
	require.NoError(t, err)
	return &stampFx{rematchFx: f, tk: tk, rec: rec, at: f.now.Add(-time.Minute)}
}

func (f *stampFx) stamp(t *testing.T, id uuid.UUID, want ticket.SLAStampRequest) (ticket.SLAStamped, error) {
	t.Helper()
	want.At = f.at
	return f.ts.StampSLACrossings(f.ctx, id, want)
}

func (f *stampFx) row(t *testing.T) dbgen.SlaRecord {
	t.Helper()
	r, err := f.q.GetSLARecord(f.ctx, f.tk.ID)
	require.NoError(t, err)
	return r
}

func TestStampSLACrossings_ReportsWhatItNewlySet(t *testing.T) {
	cases := []struct {
		name string
		want ticket.SLAStampRequest
		got  ticket.SLAStamped
	}{
		{"nothing asked", ticket.SLAStampRequest{}, ticket.SLAStamped{}},
		{"response breach", ticket.SLAStampRequest{ResponseBreach: true}, ticket.SLAStamped{ResponseBreach: true}},
		{"resolution breach", ticket.SLAStampRequest{ResolutionBreach: true}, ticket.SLAStamped{ResolutionBreach: true}},
		{"response at risk", ticket.SLAStampRequest{ResponseAtRisk: true}, ticket.SLAStamped{ResponseAtRisk: true}},
		{"resolution at risk", ticket.SLAStampRequest{ResolutionAtRisk: true}, ticket.SLAStamped{ResolutionAtRisk: true}},
		{
			"both breaches",
			ticket.SLAStampRequest{ResponseBreach: true, ResolutionBreach: true},
			ticket.SLAStamped{ResponseBreach: true, ResolutionBreach: true},
		},
		{
			"a breach and the other target at risk",
			ticket.SLAStampRequest{ResponseBreach: true, ResolutionAtRisk: true},
			ticket.SLAStamped{ResponseBreach: true, ResolutionAtRisk: true},
		},
		{
			"both at risk",
			ticket.SLAStampRequest{ResponseAtRisk: true, ResolutionAtRisk: true},
			ticket.SLAStamped{ResponseAtRisk: true, ResolutionAtRisk: true},
		},
		{
			// An at-risk stamp is only for a target below 100%: a breach of the
			// same target in the same statement leaves it unset.
			"a target asked for at risk and breached in one statement is breached only",
			ticket.SLAStampRequest{ResponseBreach: true, ResponseAtRisk: true},
			ticket.SLAStamped{ResponseBreach: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newStampFx(t)
			got, err := f.stamp(t, f.rec.ID, tc.want)
			require.NoError(t, err)
			require.Equal(t, tc.got, got)

			// The columns say the same thing as the answer, at the instant given,
			// and nothing else on the row moved.
			r := f.row(t)
			set := func(c sql.NullTime) bool {
				if !c.Valid {
					return false
				}
				require.True(t, c.Time.Equal(f.at), "stamped with the instant it was given")
				return true
			}
			require.Equal(t, tc.got, ticket.SLAStamped{
				ResponseBreach:   set(r.ResponseBreachedAt),
				ResolutionBreach: set(r.ResolutionBreachedAt),
				ResponseAtRisk:   set(r.ResponseAtRiskAt),
				ResolutionAtRisk: set(r.ResolutionAtRiskAt),
			})
			require.False(t, r.FirstResponseAt.Valid)
			require.False(t, r.ResolvedAt.Valid)
			require.False(t, r.SupersededAt.Valid)
			require.Equal(t, f.rec.ID, r.ID)
		})
	}
}

func TestStampSLACrossings_AColumnAlreadySetIsNotSetAgain(t *testing.T) {
	first := time.Now().UTC().Truncate(time.Millisecond).Add(-time.Hour)
	all := ticket.SLAStampRequest{ResponseBreach: true, ResolutionBreach: true, ResponseAtRisk: true, ResolutionAtRisk: true}

	t.Run("a second identical stamp sets nothing", func(t *testing.T) {
		f := newStampFx(t)
		req := ticket.SLAStampRequest{ResponseBreach: true, ResolutionAtRisk: true}
		got, err := f.stamp(t, f.rec.ID, req)
		require.NoError(t, err)
		require.Equal(t, ticket.SLAStamped{ResponseBreach: true, ResolutionAtRisk: true}, got)

		f.at = f.at.Add(10 * time.Minute)
		got, err = f.stamp(t, f.rec.ID, req)
		require.NoError(t, err)
		require.Equal(t, ticket.SLAStamped{}, got)
	})

	t.Run("a stamp already set keeps its instant", func(t *testing.T) {
		f := newStampFx(t)
		require.NoError(t, f.ss.StampBreaches(f.ctx, f.rec.ID, &first, nil))
		got, err := f.stamp(t, f.rec.ID, ticket.SLAStampRequest{ResponseBreach: true, ResolutionBreach: true})
		require.NoError(t, err)
		require.Equal(t, ticket.SLAStamped{ResolutionBreach: true}, got, "only the column that was still NULL")
		r := f.row(t)
		require.True(t, r.ResponseBreachedAt.Time.Equal(first), "the first stamp wins")
		require.True(t, r.ResolutionBreachedAt.Time.Equal(f.at))
	})

	t.Run("a second pass sets only what the first did not", func(t *testing.T) {
		f := newStampFx(t)
		_, err := f.stamp(t, f.rec.ID, ticket.SLAStampRequest{ResponseAtRisk: true})
		require.NoError(t, err)
		got, err := f.stamp(t, f.rec.ID, all)
		require.NoError(t, err)
		// The at-risk response stamp is set already, and a breach asked for in
		// this same statement leaves the response at-risk stamp alone either way.
		require.Equal(t, ticket.SLAStamped{ResponseBreach: true, ResolutionBreach: true}, got)
	})

	t.Run("a breach stamped earlier blocks an at-risk stamp of that target", func(t *testing.T) {
		f := newStampFx(t)
		require.NoError(t, f.ss.StampBreaches(f.ctx, f.rec.ID, &first, &first))
		got, err := f.stamp(t, f.rec.ID, ticket.SLAStampRequest{ResponseAtRisk: true, ResolutionAtRisk: true})
		require.NoError(t, err)
		require.Equal(t, ticket.SLAStamped{}, got, "at risk never alerts once the same target has breached")
		r := f.row(t)
		require.False(t, r.ResponseAtRiskAt.Valid)
		require.False(t, r.ResolutionAtRiskAt.Valid)
	})

	t.Run("a breach of one target does not block the other target's at-risk stamp", func(t *testing.T) {
		f := newStampFx(t)
		require.NoError(t, f.ss.StampBreaches(f.ctx, f.rec.ID, &first, nil))
		got, err := f.stamp(t, f.rec.ID, ticket.SLAStampRequest{ResponseAtRisk: true, ResolutionAtRisk: true})
		require.NoError(t, err)
		require.Equal(t, ticket.SLAStamped{ResolutionAtRisk: true}, got)
	})
}

func TestStampSLACrossings_AMetTargetIsNotStamped(t *testing.T) {
	all := ticket.SLAStampRequest{ResponseBreach: true, ResolutionBreach: true, ResponseAtRisk: true, ResolutionAtRisk: true}

	t.Run("a response met before the stamp", func(t *testing.T) {
		f := newStampFx(t)
		// Met on time, with room to spare: the reply's own statement stamps no breach.
		require.NoError(t, f.ss.SetFirstResponse(f.ctx, f.rec.ID, f.now.Add(-2*time.Hour), 60, 1800))
		got, err := f.stamp(t, f.rec.ID, ticket.SLAStampRequest{ResponseBreach: true, ResponseAtRisk: true})
		require.NoError(t, err)
		require.Equal(t, ticket.SLAStamped{}, got)
		r := f.row(t)
		require.False(t, r.ResponseBreachedAt.Valid)
		require.False(t, r.ResponseAtRiskAt.Valid)

		// The other target is still outstanding and is stamped.
		got, err = f.stamp(t, f.rec.ID, all)
		require.NoError(t, err)
		require.Equal(t, ticket.SLAStamped{ResolutionBreach: true}, got)
	})

	t.Run("a resolution met before the stamp", func(t *testing.T) {
		f := newStampFx(t)
		require.NoError(t, f.ss.SetResolved(f.ctx, f.rec.ID, f.now.Add(-2*time.Hour), 60, 7200))
		got, err := f.stamp(t, f.rec.ID, ticket.SLAStampRequest{ResolutionBreach: true, ResolutionAtRisk: true})
		require.NoError(t, err)
		require.Equal(t, ticket.SLAStamped{}, got)
		r := f.row(t)
		require.False(t, r.ResolutionBreachedAt.Valid)
		require.False(t, r.ResolutionAtRiskAt.Valid)

		got, err = f.stamp(t, f.rec.ID, all)
		require.NoError(t, err)
		require.Equal(t, ticket.SLAStamped{ResponseBreach: true}, got)
	})
}

func TestStampSLACrossings_ASupersededRecordIsNotWritten(t *testing.T) {
	all := ticket.SLAStampRequest{ResponseBreach: true, ResolutionBreach: true, ResponseAtRisk: true, ResolutionAtRisk: true}

	t.Run("superseded after the caller read it", func(t *testing.T) {
		f := newStampFx(t)
		f.supersede(t, f.tk.ID, f.now)
		got, err := f.stamp(t, f.rec.ID, all)
		require.ErrorIs(t, err, ticket.ErrSLARecordSuperseded)
		require.ErrorIs(t, err, sla.ErrRecordSuperseded, "one error for one condition")
		require.Equal(t, ticket.SLAStamped{}, got)

		rows, err := f.q.ListSLARecordsForTicket(f.ctx, f.tk.ID)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		r := rows[0]
		require.False(t, r.ResponseBreachedAt.Valid || r.ResolutionBreachedAt.Valid ||
			r.ResponseAtRiskAt.Valid || r.ResolutionAtRiskAt.Valid, "a superseded record keeps its own stamps and gets none")
	})

	t.Run("the replacement is not stamped through the old id", func(t *testing.T) {
		f := newStampFx(t)
		oldID := f.supersede(t, f.tk.ID, f.now)
		newID := f.insert(t, f.tk.ID, f.rec.PolicyID, nil, 0)
		_, err := f.stamp(t, oldID, all)
		require.ErrorIs(t, err, ticket.ErrSLARecordSuperseded)
		r := f.row(t)
		require.Equal(t, newID, r.ID)
		require.False(t, r.ResponseBreachedAt.Valid || r.ResolutionBreachedAt.Valid ||
			r.ResponseAtRiskAt.Valid || r.ResolutionAtRiskAt.Valid)
	})

	t.Run("an id that names no record", func(t *testing.T) {
		f := newStampFx(t)
		_, err := f.stamp(t, uuid.New(), all)
		require.ErrorIs(t, err, ticket.ErrSLARecordSuperseded)
	})
}

// A re-match copies the at-risk stamps exactly as it copies the breach stamps,
// so a crossing already announced is not announced again.
func TestStartSLARecord_CarriesTheAtRiskStamps(t *testing.T) {
	f := newStampFx(t)
	_, err := f.stamp(t, f.rec.ID, ticket.SLAStampRequest{ResponseAtRisk: true, ResolutionBreach: true})
	require.NoError(t, err)

	cur, err := f.ts.CurrentSLARecordForUpdate(f.ctx, f.tk.ID)
	require.NoError(t, err)
	require.NotNil(t, cur.Facts.ResponseAtRiskAt)
	require.True(t, cur.Facts.ResponseAtRiskAt.Equal(f.at))
	require.Nil(t, cur.Facts.ResolutionAtRiskAt)
	require.NotNil(t, cur.Facts.ResolutionBreachedAt)

	require.NoError(t, f.ts.SupersedeSLARecord(f.ctx, cur.ID, f.now))
	next := ticket.SLARecordStart{ID: uuid.New(), TicketID: f.tk.ID, PolicyID: f.rec.PolicyID, Facts: cur.Facts}
	require.NoError(t, f.ts.StartSLARecord(f.ctx, next))

	got, err := f.ss.GetRecord(f.ctx, f.tk.ID)
	require.NoError(t, err)
	require.Equal(t, next.ID, got.ID)
	require.NotNil(t, got.ResponseAtRiskAt, "read back onto sla.Record")
	require.True(t, got.ResponseAtRiskAt.Equal(f.at))
	require.Nil(t, got.ResolutionAtRiskAt)
}
