package emailtemplatestore_test

import (
	"context"
	"errors"
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

// The HTML part is optional and "none" is NULL, not an empty string: a row
// saved without HTML must read back as having no HTML part, so its mail stays
// plain text.
func TestApply_HTMLRoundTripsAndNoneIsNull(t *testing.T) {
	s, q, name := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.Apply(ctx, name, &emailtemplate.Override{Name: name, Subject: "s", Body: "b", HTML: "<p>h</p>"}, entryFor(name, "saved")))
	got, err := s.Overrides(ctx, name)
	require.NoError(t, err)
	require.Equal(t, "<p>h</p>", got[name].HTML)

	var prevHTML string
	require.NoError(t, s.Apply(ctx, name, &emailtemplate.Override{Name: name, Subject: "s", Body: "b"},
		func(prev *emailtemplate.Override) *audit.Entry {
			prevHTML = prev.HTML
			return entryFor(name, "saved")(prev)
		}))
	require.Equal(t, "<p>h</p>", prevHTML, "the entry is told the HTML that was there")

	got, _ = s.Overrides(ctx, name)
	require.Empty(t, got[name].HTML)
	row, err := q.LockEmailTemplateOverride(ctx, name)
	require.NoError(t, err)
	require.False(t, row.Html.Valid, "no HTML is stored as NULL")
}

// Update runs the decision INSIDE the transaction and the row lock, so a save
// that merges with what is stored (an absent html key) merges with the row as it
// is when it commits, not as it was when the request was read.
func TestUpdate_TheDecisionSeesTheLockedRowAndSerialisesWithOthers(t *testing.T) {
	s, _, name := newStore(t)
	ctx := context.Background()
	require.NoError(t, s.Apply(ctx, name, &emailtemplate.Override{Name: name, Subject: "s", Body: "b0", HTML: "<p>first</p>"}, entryFor(name, "saved")))

	// A holds the lock until released, having read HTML "<p>first</p>".
	aRead := make(chan string, 1)
	release := make(chan struct{})
	aDone := make(chan error, 1)
	go func() {
		aDone <- s.Update(ctx, name, func(prev *emailtemplate.Override) (*emailtemplate.Override, *audit.Entry, error) {
			aRead <- prev.HTML
			<-release
			return &emailtemplate.Override{Name: name, Subject: "s", Body: "bA", HTML: prev.HTML + "A"}, entryFor(name, "saved")(prev), nil
		})
	}()
	require.Equal(t, "<p>first</p>", <-aRead)

	// B arrives while A holds the row. It must wait, and then see A's result.
	bSaw := make(chan string, 1)
	bDone := make(chan error, 1)
	go func() {
		bDone <- s.Update(ctx, name, func(prev *emailtemplate.Override) (*emailtemplate.Override, *audit.Entry, error) {
			bSaw <- prev.HTML
			return &emailtemplate.Override{Name: name, Subject: "s", Body: "bB", HTML: prev.HTML + "B"}, entryFor(name, "saved")(prev), nil
		})
	}()
	select {
	case <-bSaw:
		t.Fatal("B read the row while A held it: a merge in B would bring back stale HTML")
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-aDone)
	require.Equal(t, "<p>first</p>A", <-bSaw, "B merged with A's committed row")
	require.NoError(t, <-bDone)

	got, err := s.Overrides(ctx, name)
	require.NoError(t, err)
	require.Equal(t, "<p>first</p>AB", got[name].HTML)
}

func TestUpdate_ADecisionToRemoveDeletesAndAnErrorChangesNothing(t *testing.T) {
	s, _, name := newStore(t)
	ctx := context.Background()
	require.NoError(t, s.Apply(ctx, name, &emailtemplate.Override{Name: name, Subject: "s", Body: "b"}, entryFor(name, "saved")))

	boom := errors.New("refused")
	err := s.Update(ctx, name, func(*emailtemplate.Override) (*emailtemplate.Override, *audit.Entry, error) { return nil, nil, boom })
	require.ErrorIs(t, err, boom)
	got, _ := s.Overrides(ctx, name)
	require.Len(t, got, 1, "a refused decision leaves the row")

	require.NoError(t, s.Update(ctx, name, func(prev *emailtemplate.Override) (*emailtemplate.Override, *audit.Entry, error) {
		return nil, entryFor(name, "reverted")(prev), nil
	}))
	got, _ = s.Overrides(ctx, name)
	require.Empty(t, got)

	var prevSeen = true
	require.NoError(t, s.Update(ctx, name, func(prev *emailtemplate.Override) (*emailtemplate.Override, *audit.Entry, error) {
		prevSeen = prev != nil
		return nil, nil, nil
	}))
	require.False(t, prevSeen, "no row is a nil prev")
}
