package database_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// InSnapshot is what makes the KPI page one set of numbers: every query of a
// request reads the database as it was when the first began, however many
// rows are written between them. Against a real pool, since a harness
// transaction shows neither isolation nor a second connection.
func TestInSnapshot(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ctx := context.Background()
	q := dbgen.New(db.SQL)

	t.Run("later commits are not seen by a later read", func(t *testing.T) {
		var before, during, after int
		require.NoError(t, database.InSnapshot(ctx, db.SQL, q, func(tq *dbgen.Queries) error {
			rows, err := tq.ListStatuses(ctx)
			require.NoError(t, err)
			before = len(rows)

			// Another connection commits a row between the two reads.
			require.NoError(t, q.CreateStatus(ctx, dbgen.CreateStatusParams{
				ID: uuid.New(), Name: "Snapshot " + uuid.NewString(), Kind: "custom", Color: "#000000",
			}))

			rows, err = tq.ListStatuses(ctx)
			require.NoError(t, err)
			during = len(rows)
			return nil
		}))
		rows, err := q.ListStatuses(ctx)
		require.NoError(t, err)
		after = len(rows)
		require.Equal(t, before, during, "the second read saw a row committed after the first")
		require.Equal(t, before+1, after, "the row is there for the next reader")
	})

	t.Run("cannot write", func(t *testing.T) {
		err := database.InSnapshot(ctx, db.SQL, q, func(tq *dbgen.Queries) error {
			return tq.CreateStatus(ctx, dbgen.CreateStatusParams{
				ID: uuid.New(), Name: "Read only " + uuid.NewString(), Kind: "custom", Color: "#000000",
			})
		})
		require.ErrorContains(t, err, "read-only transaction")
	})

	t.Run("returns fn's error", func(t *testing.T) {
		boom := errors.New("refused")
		require.ErrorIs(t, database.InSnapshot(ctx, db.SQL, q, func(*dbgen.Queries) error { return boom }), boom)
	})

	t.Run("a nil db runs fn on the queries it was given", func(t *testing.T) {
		var got *dbgen.Queries
		require.NoError(t, database.InSnapshot(ctx, nil, q, func(tq *dbgen.Queries) error { got = tq; return nil }))
		require.Same(t, q, got)
	})
}
