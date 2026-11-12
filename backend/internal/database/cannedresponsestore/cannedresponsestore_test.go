package cannedresponsestore_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/cannedresponsestore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/cannedresponse"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// newStore gives each test its own rolled-back transaction. A foreign-key
// violation aborts the transaction it happens in, so the cases below that
// provoke one run one to a store.
func newStore(t *testing.T) (*cannedresponsestore.Store, *sql.Tx, func()) {
	t.Helper()
	db, closeDB := testutil.NewDB(t)
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	return cannedresponsestore.New(q), tx, func() { rollback(); closeDB() }
}

func seedCategory(t *testing.T, tx *sql.Tx) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, tx.QueryRow(`INSERT INTO categories (name) VALUES ($1) RETURNING id`, uuid.NewString()).Scan(&id))
	return id
}

func seedType(t *testing.T, tx *sql.Tx, categoryID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, tx.QueryRow(`INSERT INTO types (category_id, name) VALUES ($1, $2) RETURNING id`, categoryID, uuid.NewString()).Scan(&id))
	return id
}

// #528: a delete that touches no row says so.
func TestDelete_UnknownIDIsNotFound(t *testing.T) {
	s, _, done := newStore(t)
	defer done()

	require.ErrorIs(t, s.Delete(context.Background(), uuid.New()), cannedresponse.ErrNotFound)
}

func TestDelete_RemovesTheRow(t *testing.T) {
	s, _, done := newStore(t)
	defer done()
	ctx := context.Background()

	cr := cannedresponse.CannedResponse{ID: uuid.New(), Name: "n", Body: "b"}
	require.NoError(t, s.Create(ctx, cr))
	require.NoError(t, s.Delete(ctx, cr.ID))
	_, err := s.Get(ctx, cr.ID)
	require.ErrorIs(t, err, cannedresponse.ErrNotFound)
}

// A scope that names nothing, or a type from another category, is the caller's
// value. Each of the three foreign keys is recognised by name.
func TestCreateAndUpdate_UnknownScopeIsErrUnknownScope(t *testing.T) {
	missing := uuid.New()
	for _, tc := range []struct {
		name  string
		scope func(t *testing.T, tx *sql.Tx) (cat, typ *uuid.UUID)
	}{
		{"unknown category", func(t *testing.T, tx *sql.Tx) (*uuid.UUID, *uuid.UUID) { return &missing, nil }},
		{"unknown type", func(t *testing.T, tx *sql.Tx) (*uuid.UUID, *uuid.UUID) {
			cat := seedCategory(t, tx)
			return &cat, &missing
		}},
		{"type from another category", func(t *testing.T, tx *sql.Tx) (*uuid.UUID, *uuid.UUID) {
			cat, other := seedCategory(t, tx), seedCategory(t, tx)
			typ := seedType(t, tx, other)
			return &cat, &typ
		}},
	} {
		for _, verb := range []string{"create", "update"} {
			t.Run(verb+": "+tc.name, func(t *testing.T) {
				s, tx, done := newStore(t)
				defer done()
				ctx := context.Background()

				cat, typ := tc.scope(t, tx)
				cr := cannedresponse.CannedResponse{ID: uuid.New(), Name: "n", Body: "b"}
				if verb == "update" {
					require.NoError(t, s.Create(ctx, cr))
				}
				cr.CategoryID, cr.TypeID = cat, typ

				var err error
				if verb == "create" {
					err = s.Create(ctx, cr)
				} else {
					err = s.Update(ctx, cr)
				}
				require.ErrorIs(t, err, cannedresponse.ErrUnknownScope)
			})
		}
	}
}
