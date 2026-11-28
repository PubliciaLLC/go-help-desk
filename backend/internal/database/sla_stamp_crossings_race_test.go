package database_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// Every replica runs a sweep, so two statements can decide the same crossing.
// The row lock puts one after the other and exactly one sets each column.
func TestStampSLACrossings_ConcurrentStampersSetEachColumnOnce(t *testing.T) {
	f := newChangePriorityFx(t) // committed rows, a database of its own
	ctx := context.Background()
	const stampers = 9

	// Different stampers ask for different mixes, so the columns race each other
	// as well as themselves. A breach that lands first keeps that target's
	// at-risk stamp out; an at-risk stamp that lands first does not stop the
	// breach. Either order leaves each breach set once and each at-risk set at
	// most once.
	reqs := []ticket.SLAStampRequest{
		{ResponseBreach: true, ResolutionAtRisk: true},
		{ResponseAtRisk: true, ResolutionBreach: true},
		{ResponseBreach: true, ResolutionBreach: true},
	}

	for round := 0; round < 5; round++ {
		tk := f.tk
		tk.ID = uuid.New()
		tk.TrackingNumber = ticket.TrackingNumber("CS-" + uuid.NewString()[:8])
		require.NoError(t, f.ts.Create(ctx, tk))
		require.NoError(t, f.ss.CreateRecord(ctx, sla.Record{TicketID: tk.ID, PolicyID: f.pol[ticket.PriorityMedium].ID}))
		rec, err := f.ss.GetRecord(ctx, tk.ID)
		require.NoError(t, err)

		var (
			wg    sync.WaitGroup
			start = make(chan struct{})
			got   = make([]ticket.SLAStamped, stampers)
			errs  = make([]error, stampers)
		)
		for i := 0; i < stampers; i++ {
			req := reqs[i%len(reqs)]
			req.At = time.Now().UTC()
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				tx, err := f.db.SQL.BeginTx(ctx, nil)
				if err != nil {
					errs[i] = err
					return
				}
				got[i], err = ticketstore.New(dbgen.New(tx)).StampSLACrossings(ctx, rec.ID, req)
				if err != nil {
					_ = tx.Rollback()
					errs[i] = err
					return
				}
				errs[i] = tx.Commit()
			}()
		}
		close(start)
		wg.Wait()
		for _, err := range errs {
			require.NoError(t, err)
		}

		var n struct{ respBreach, resBreach, respRisk, resRisk int }
		for _, s := range got {
			if s.ResponseBreach {
				n.respBreach++
			}
			if s.ResolutionBreach {
				n.resBreach++
			}
			if s.ResponseAtRisk {
				n.respRisk++
			}
			if s.ResolutionAtRisk {
				n.resRisk++
			}
		}
		row, err := f.q.GetSLARecord(ctx, tk.ID)
		require.NoError(t, err)
		// Each breach was asked for by someone and set by exactly one stamper.
		require.Equal(t, 1, n.respBreach, "round %d", round)
		require.Equal(t, 1, n.resBreach, "round %d", round)
		// An at-risk stamp is set by at most one, and only if it beat the breach.
		require.LessOrEqual(t, n.respRisk, 1, "round %d", round)
		require.LessOrEqual(t, n.resRisk, 1, "round %d", round)
		// A column is set exactly when one stamper reported setting it.
		require.True(t, row.ResponseBreachedAt.Valid)
		require.True(t, row.ResolutionBreachedAt.Valid)
		require.Equal(t, n.respRisk == 1, row.ResponseAtRiskAt.Valid, "round %d", round)
		require.Equal(t, n.resRisk == 1, row.ResolutionAtRiskAt.Valid, "round %d", round)
	}
}
