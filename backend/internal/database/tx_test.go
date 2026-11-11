package database_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// InTx is what lets a first-factor write take a row lock and keep it until it
// has written, so what it commits and rolls back is the whole of its contract.
// Against a real pool, since a rolled-back harness transaction cannot show a
// commit.
func TestInTx(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ctx := context.Background()
	q := dbgen.New(db.SQL)

	entityID := uuid.New()
	t.Cleanup(func() { _, _ = db.SQL.Exec(`DELETE FROM audit_log WHERE entity_id = $1`, entityID) })
	insert := func(q *dbgen.Queries) error {
		return q.CreateAuditEntry(ctx, dbgen.CreateAuditEntryParams{
			ID: uuid.New(), EntityType: "intx-test", EntityID: entityID, Action: "x", CreatedAt: time.Now(),
		})
	}
	visible := func() int {
		var n int
		require.NoError(t, db.SQL.QueryRow(`SELECT count(*) FROM audit_log WHERE entity_id = $1`, entityID).Scan(&n))
		return n
	}

	t.Run("commits what fn wrote on the transaction, and not before", func(t *testing.T) {
		require.NoError(t, database.InTx(ctx, db.SQL, q, func(tq *dbgen.Queries) error {
			require.NoError(t, insert(tq))
			require.Zero(t, visible(), "the row was visible outside before the commit: fn ran on the pool")
			return nil
		}))
		require.Equal(t, 1, visible())
	})

	t.Run("rolls back and returns fn's error", func(t *testing.T) {
		boom := errors.New("refused")
		before := visible()
		err := database.InTx(ctx, db.SQL, q, func(tq *dbgen.Queries) error {
			if err := insert(tq); err != nil {
				return err
			}
			return boom
		})
		require.ErrorIs(t, err, boom)
		require.Equal(t, before, visible(), "a refused transaction left its row behind")
	})

	t.Run("a nil db runs fn on the queries it was given", func(t *testing.T) {
		var got *dbgen.Queries
		require.NoError(t, database.InTx(ctx, nil, q, func(tq *dbgen.Queries) error { got = tq; return nil }))
		require.Same(t, q, got)
	})
}
