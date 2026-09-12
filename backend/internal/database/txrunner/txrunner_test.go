package txrunner_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/txrunner"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// These run against a real Postgres because the thing under test IS the
// transaction. A fake would assert that the code calls Commit, not that the
// database honoured it — and rollback semantics are exactly what a fake cannot
// demonstrate.
//
// Every case uses a freshly generated entity_id and asserts only on rows
// carrying it, so the tests neither interfere with each other nor depend on a
// clean database. audit_log is used rather than tickets because its only
// foreign key is nullable, so no fixture scaffolding is needed to reach the
// behaviour being tested.

var errCallerFailed = errors.New("the caller failed after writing")

func entry(entityID uuid.UUID, action string) audit.Entry {
	return audit.Entry{
		ID:         uuid.New(),
		EntityType: "txrunner-test",
		EntityID:   entityID,
		Action:     action,
		CreatedAt:  time.Now(),
	}
}

// visible counts committed rows for an entity, read outside any transaction the
// test itself holds.
func visible(t *testing.T, db *testutil.DB, entityID uuid.UUID) int {
	t.Helper()
	q := auditstore.New(db.Queries)
	entries, err := q.ListByEntity(context.Background(), "txrunner-test", entityID, 100, 0)
	require.NoError(t, err)
	return len(entries)
}

func TestInTx_CommitsWhenTheCallerSucceeds(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()

	r := txrunner.New(db.SQL)
	entityID := uuid.New()

	err := r.InTx(context.Background(), func(_ ticket.Store, au audit.Store) error {
		return au.Create(context.Background(), entry(entityID, "committed"))
	})
	require.NoError(t, err)

	require.Equal(t, 1, visible(t, db, entityID), "a successful InTx must commit")
}

// TestInTx_RollsBackWhenTheCallerFails is the case the whole change exists for.
func TestInTx_RollsBackWhenTheCallerFails(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()

	r := txrunner.New(db.SQL)
	entityID := uuid.New()

	err := r.InTx(context.Background(), func(_ ticket.Store, au audit.Store) error {
		// Write first, then fail — the write must not survive.
		if err := au.Create(context.Background(), entry(entityID, "should-vanish")); err != nil {
			return err
		}
		return errCallerFailed
	})

	require.ErrorIs(t, err, errCallerFailed, "the caller's error must propagate unchanged")
	require.Equal(t, 0, visible(t, db, entityID), "a failed InTx must leave nothing behind")
}

// TestInTx_RollsBackEveryWrite covers the multi-write case, which is the shape
// the ticket service actually uses: several rows, one of them failing.
func TestInTx_RollsBackEveryWrite(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()

	r := txrunner.New(db.SQL)
	entityID := uuid.New()

	err := r.InTx(context.Background(), func(_ ticket.Store, au audit.Store) error {
		for _, action := range []string{"first", "second", "third"} {
			if err := au.Create(context.Background(), entry(entityID, action)); err != nil {
				return err
			}
		}
		return errCallerFailed
	})

	require.Error(t, err)
	require.Equal(t, 0, visible(t, db, entityID),
		"all writes in the transaction must be undone, not just the last")
}

// TestInTx_IsolatesSuccessFromFailure pins that a rollback does not take an
// earlier committed transaction with it — the runner must begin a fresh
// transaction per call rather than reusing one.
func TestInTx_IsolatesSuccessFromFailure(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()

	r := txrunner.New(db.SQL)
	kept := uuid.New()
	discarded := uuid.New()

	require.NoError(t, r.InTx(context.Background(), func(_ ticket.Store, au audit.Store) error {
		return au.Create(context.Background(), entry(kept, "kept"))
	}))

	require.Error(t, r.InTx(context.Background(), func(_ ticket.Store, au audit.Store) error {
		if err := au.Create(context.Background(), entry(discarded, "discarded")); err != nil {
			return err
		}
		return errCallerFailed
	}))

	require.Equal(t, 1, visible(t, db, kept), "the earlier commit must survive")
	require.Equal(t, 0, visible(t, db, discarded))
}

// TestInTx_PassesBothStores checks the reason fn takes two stores: they must be
// bound to the same transaction, or an audit entry could commit while the
// change it describes rolls back.
func TestInTx_PassesBothStores(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()

	r := txrunner.New(db.SQL)

	require.NoError(t, r.InTx(context.Background(), func(st ticket.Store, au audit.Store) error {
		require.NotNil(t, st, "the ticket store must be bound to the transaction")
		require.NotNil(t, au, "the audit store must be bound to the transaction")
		return nil
	}))
}
