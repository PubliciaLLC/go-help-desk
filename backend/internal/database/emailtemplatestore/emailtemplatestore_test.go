package emailtemplatestore_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/emailtemplatestore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/emailtemplate"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// These commit for real (the store opens its own transaction, which is the
// thing under test), so each uses a name of its own and removes it after.

func newStore(t *testing.T) (*emailtemplatestore.Store, *dbgen.Queries, string) {
	t.Helper()
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)
	name := "zz_store_" + uuid.NewString()
	t.Cleanup(func() {
		_, _ = db.SQL.Exec(`DELETE FROM email_template_overrides WHERE name = $1`, name)
		_, _ = db.SQL.Exec(`DELETE FROM audit_log WHERE entity_type = 'email_template_test' AND after->>'template_name' = $1`, name)
	})
	return emailtemplatestore.NewWithDB(db.SQL), dbgen.New(db.SQL), name
}

func entryFor(name, action string) func(prev *emailtemplate.Override) *audit.Entry {
	return func(prev *emailtemplate.Override) *audit.Entry {
		before := map[string]any(nil)
		if prev != nil {
			before = map[string]any{"template_body": prev.Body}
		}
		return &audit.Entry{
			ID: uuid.New(), EntityType: "email_template_test", EntityID: uuid.New(), Action: action,
			Before: before, After: map[string]any{"template_name": name}, CreatedAt: time.Now(),
		}
	}
}

func TestOverrides_AbsentNameIsAbsent(t *testing.T) {
	s, _, name := newStore(t)
	got, err := s.Overrides(context.Background(), name, "zz_other")
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestApply_SetsReplacesAndRemoves(t *testing.T) {
	s, _, name := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.Apply(ctx, name, &emailtemplate.Override{Name: name, Subject: "s1", Body: "b1"}, entryFor(name, "saved")))
	got, err := s.Overrides(ctx, name)
	require.NoError(t, err)
	require.Equal(t, "s1", got[name].Subject)
	require.Equal(t, "b1", got[name].Body)
	require.False(t, got[name].UpdatedAt.IsZero())

	var seenPrev *emailtemplate.Override
	require.NoError(t, s.Apply(ctx, name, &emailtemplate.Override{Name: name, Subject: "s2", Body: "b2"},
		func(prev *emailtemplate.Override) *audit.Entry { seenPrev = prev; return entryFor(name, "saved")(prev) }))
	require.NotNil(t, seenPrev, "the entry is told what was there")
	require.Equal(t, "b1", seenPrev.Body)
	got, _ = s.Overrides(ctx, name)
	require.Equal(t, "b2", got[name].Body)

	require.NoError(t, s.Apply(ctx, name, nil, entryFor(name, "reverted")))
	got, _ = s.Overrides(ctx, name)
	require.Empty(t, got)
}

// In the caller's rolled-back transaction: a user committed for real would
// count as an administrator for every other package's tests running at once.
func TestApply_UpdatedByIsRecorded(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	defer rollback()
	s := emailtemplatestore.New(q)
	ctx := context.Background()

	var uid uuid.UUID
	require.NoError(t, tx.QueryRow(`INSERT INTO users (email, display_name, role) VALUES ($1, 'e', 'staff') RETURNING id`, uuid.NewString()+"@example.com").Scan(&uid))

	require.NoError(t, s.Apply(ctx, "ticket_created", &emailtemplate.Override{Name: "ticket_created", Subject: "s", Body: "b", UpdatedBy: &uid}, entryFor("ticket_created", "saved")))
	got, err := s.Overrides(ctx, "ticket_created")
	require.NoError(t, err)
	require.NotNil(t, got["ticket_created"].UpdatedBy)
	require.Equal(t, uid, *got["ticket_created"].UpdatedBy)
}

func TestApply_RevertingWhatWasNeverOverriddenWritesNoEntry(t *testing.T) {
	s, _, name := newStore(t)
	called := false
	require.NoError(t, s.Apply(context.Background(), name, nil, func(prev *emailtemplate.Override) *audit.Entry {
		called = true
		require.Nil(t, prev)
		return nil
	}))
	require.True(t, called)
}

// The row and its audit entry commit together or not at all.
func TestApply_AFailedAuditWriteRollsTheChangeBack(t *testing.T) {
	s, _, name := newStore(t)
	ctx := context.Background()
	require.NoError(t, s.Apply(ctx, name, &emailtemplate.Override{Name: name, Subject: "s1", Body: "b1"}, entryFor(name, "saved")))

	badActor := uuid.New() // no such user: the entry's foreign key fails
	err := s.Apply(ctx, name, &emailtemplate.Override{Name: name, Subject: "s2", Body: "b2"}, func(prev *emailtemplate.Override) *audit.Entry {
		e := entryFor(name, "saved")(prev)
		e.ActorID = &badActor
		return e
	})
	require.Error(t, err)
	got, _ := s.Overrides(ctx, name)
	require.Equal(t, "b1", got[name].Body, "the edit must not stand without its audit entry")

	err = s.Apply(ctx, name, nil, func(prev *emailtemplate.Override) *audit.Entry {
		e := entryFor(name, "reverted")(prev)
		e.ActorID = &badActor
		return e
	})
	require.Error(t, err)
	got, _ = s.Overrides(ctx, name)
	require.Len(t, got, 1, "the revert must not stand without its audit entry")
}

func TestApply_TheEntryIsWritten(t *testing.T) {
	s, q, name := newStore(t)
	ctx := context.Background()
	id := uuid.New()
	require.NoError(t, s.Apply(ctx, name, &emailtemplate.Override{Name: name, Subject: "s", Body: "b"}, func(prev *emailtemplate.Override) *audit.Entry {
		e := entryFor(name, "saved")(prev)
		e.EntityID = id
		return e
	}))
	got, err := auditstore.New(q).ListByEntity(ctx, "email_template_test", id, 10, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "saved", got[0].Action)
}

func TestList_ReturnsEveryRow(t *testing.T) {
	s, _, name := newStore(t)
	ctx := context.Background()
	require.NoError(t, s.Apply(ctx, name, &emailtemplate.Override{Name: name, Subject: "s", Body: "b"}, entryFor(name, "saved")))
	all, err := s.List(ctx)
	require.NoError(t, err)
	var found bool
	for _, o := range all {
		found = found || o.Name == name
	}
	require.True(t, found)
}

// Built from a transaction's Queries (the integration harness) it joins that
// transaction instead of opening one, and still writes the entry.
func TestApply_OnQueriesJoinsTheCallersTransaction(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	s := emailtemplatestore.New(q)
	ctx := context.Background()

	require.NoError(t, s.Apply(ctx, "ticket_created", &emailtemplate.Override{Name: "ticket_created", Subject: "s", Body: "b"}, entryFor("ticket_created", "saved")))
	got, err := s.Overrides(ctx, "ticket_created")
	require.NoError(t, err)
	require.Equal(t, "b", got["ticket_created"].Body)
}
