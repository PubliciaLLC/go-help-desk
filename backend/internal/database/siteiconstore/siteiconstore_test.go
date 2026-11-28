package siteiconstore_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/siteiconstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/siteicon"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

const entityType = "site_icon_store_test"

var entityID = uuid.New()

// fileset is a stand-in for the eight rendered files: the store keeps bytes, it
// does not look inside them, so a tag is enough to tell one set from another.
func fileset(tag string) siteicon.Files {
	f := siteicon.Files{siteicon.Master: []byte("master " + tag)}
	for _, n := range siteicon.Served {
		f[n] = []byte(n + " " + tag)
	}
	return f
}

func iconOf(f siteicon.Files, bg string) siteicon.Icon {
	return siteicon.Icon{
		Background: bg, Version: siteicon.Version(f), SourceSize: 1024,
		SourceSHA256: "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
	}
}

func entry(actor *uuid.UUID, action string, before map[string]any) audit.Entry {
	return audit.Entry{
		ID: uuid.New(), ActorID: actor, EntityType: entityType, EntityID: entityID, Action: action,
		Before: before, After: map[string]any{"icon_background": "#ffffff"}, CreatedAt: time.Now(),
	}
}

func auditCount(t *testing.T, q *dbgen.Queries, action string) int {
	t.Helper()
	rows, err := q.ListAuditByEntity(context.Background(), dbgen.ListAuditByEntityParams{
		EntityType: entityType, EntityID: entityID, Limit: 100,
	})
	require.NoError(t, err)
	n := 0
	for _, r := range rows {
		if r.Action == action {
			n++
		}
	}
	return n
}

// In the caller's rolled-back transaction, which is how the other tests of
// this package's neighbours (and the HTTP harness) use the store.
func inTx(t *testing.T) (*siteiconstore.Store, *dbgen.Queries) {
	t.Helper()
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)
	q, rollback := testutil.TxQueries(t, db)
	t.Cleanup(rollback)
	return siteiconstore.New(q), q
}

func TestReplace_WritesMetaFilesAndEntry(t *testing.T) {
	s, q := inTx(t)
	ctx := context.Background()

	_, ok, err := s.Get(ctx)
	require.NoError(t, err)
	require.False(t, ok, "nothing stored yet")

	first := fileset("one")
	var seenPrev *siteicon.Icon
	called := 0
	require.NoError(t, s.Replace(ctx, iconOf(first, "#ffffff"), first, func(prev *siteicon.Icon) audit.Entry {
		called++
		seenPrev = prev
		return entry(nil, "uploaded", nil)
	}))
	require.Equal(t, 1, called)
	require.Nil(t, seenPrev, "the first upload has nothing before it")
	require.Equal(t, 1, auditCount(t, q, "uploaded"))

	got, ok, err := s.Get(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "#ffffff", got.Background)
	require.Equal(t, siteicon.Version(first), got.Version)
	require.Equal(t, 1024, got.SourceSize)
	require.False(t, got.UpdatedAt.IsZero())

	for name, want := range first {
		if name == siteicon.Master {
			continue
		}
		data, version, ok, err := s.File(ctx, name)
		require.NoError(t, err)
		require.True(t, ok, name)
		require.Equal(t, want, data, name)
		require.Equal(t, got.Version, version)
	}
	_, _, ok, err = s.File(ctx, siteicon.Master)
	require.NoError(t, err)
	require.True(t, ok, "the store holds the master; the HTTP route is what never serves it")
	_, _, ok, err = s.File(ctx, "nope.png")
	require.NoError(t, err)
	require.False(t, ok)

	sizes, err := s.FileSizes(ctx)
	require.NoError(t, err)
	require.Len(t, sizes, 7)
	require.NotContains(t, sizes, siteicon.Master)
	for _, n := range siteicon.Served {
		require.Equal(t, len(first[n]), sizes[n], n)
	}

	// Replacing: the entry is told what was there, and every file changes.
	second := fileset("two")
	require.NoError(t, s.Replace(ctx, iconOf(second, "#000000"), second, func(prev *siteicon.Icon) audit.Entry {
		seenPrev = prev
		return entry(nil, "uploaded", nil)
	}))
	require.NotNil(t, seenPrev)
	require.Equal(t, "#ffffff", seenPrev.Background)
	require.Equal(t, siteicon.Version(first), seenPrev.Version)
	require.Equal(t, 2, auditCount(t, q, "uploaded"))
	data, version, ok, err := s.File(ctx, "icon-192.png")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, second["icon-192.png"], data)
	require.Equal(t, siteicon.Version(second), version)
}

func TestReplace_RefusesAnIncompleteOrForeignSet(t *testing.T) {
	s, q := inTx(t)
	ctx := context.Background()

	missing := fileset("x")
	delete(missing, "icon-512.png")
	extra := fileset("x")
	extra["evil.svg"] = []byte("<svg/>")
	for name, files := range map[string]siteicon.Files{"a file missing": missing, "a file nobody asked for": extra} {
		err := s.Replace(ctx, iconOf(files, "#ffffff"), files, func(*siteicon.Icon) audit.Entry { return entry(nil, "uploaded", nil) })
		require.Error(t, err, name)
	}
	_, ok, err := s.Get(ctx)
	require.NoError(t, err)
	require.False(t, ok)
	require.Zero(t, auditCount(t, q, "uploaded"))
}

func TestUpdate(t *testing.T) {
	s, q := inTx(t)
	ctx := context.Background()

	found, err := s.Update(ctx, func(siteicon.Icon, siteicon.Files) (siteicon.Icon, siteicon.Files, audit.Entry, error) {
		t.Fatal("decide must not run when nothing is stored")
		return siteicon.Icon{}, nil, audit.Entry{}, nil
	})
	require.NoError(t, err)
	require.False(t, found)

	first := fileset("one")
	require.NoError(t, s.Replace(ctx, iconOf(first, "#ffffff"), first, func(*siteicon.Icon) audit.Entry { return entry(nil, "uploaded", nil) }))

	// decide sees what is stored.
	var seen siteicon.Files
	found, err = s.Update(ctx, func(prev siteicon.Icon, stored siteicon.Files) (siteicon.Icon, siteicon.Files, audit.Entry, error) {
		seen = stored
		require.Equal(t, "#ffffff", prev.Background)
		return siteicon.Icon{}, nil, audit.Entry{}, siteicon.ErrUnchanged
	})
	require.NoError(t, err, "ErrUnchanged is not an error")
	require.True(t, found)
	require.Equal(t, first, seen, "all eight files, master included")
	require.Zero(t, auditCount(t, q, "changed"), "unchanged writes no entry")

	// A change writes the files, the metadata and the entry.
	next := fileset("two")
	next[siteicon.Master] = first[siteicon.Master] // what a recolour carries over
	found, err = s.Update(ctx, func(prev siteicon.Icon, _ siteicon.Files) (siteicon.Icon, siteicon.Files, audit.Entry, error) {
		n := prev
		n.Background, n.Version = "#112233", siteicon.Version(next)
		return n, next, entry(nil, "changed", nil), nil
	})
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, 1, auditCount(t, q, "changed"))
	got, _, err := s.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, "#112233", got.Background)
	require.Equal(t, siteicon.Version(next), got.Version)
	require.Equal(t, 1024, got.SourceSize, "what decide kept")

	// An error from decide writes nothing.
	boom := errors.New("boom")
	_, err = s.Update(ctx, func(prev siteicon.Icon, _ siteicon.Files) (siteicon.Icon, siteicon.Files, audit.Entry, error) {
		return prev, nil, audit.Entry{}, boom
	})
	require.ErrorIs(t, err, boom)
	got, _, _ = s.Get(ctx)
	require.Equal(t, "#112233", got.Background)
}

func TestDelete(t *testing.T) {
	s, q := inTx(t)
	ctx := context.Background()

	deleted, err := s.Delete(ctx, func(siteicon.Icon) audit.Entry {
		t.Fatal("no entry is built when there is nothing to delete")
		return audit.Entry{}
	})
	require.NoError(t, err)
	require.False(t, deleted)
	require.Zero(t, auditCount(t, q, "removed"), "deleting nothing writes no entry")

	f := fileset("one")
	require.NoError(t, s.Replace(ctx, iconOf(f, "#abcdef"), f, func(*siteicon.Icon) audit.Entry { return entry(nil, "uploaded", nil) }))

	var seen siteicon.Icon
	deleted, err = s.Delete(ctx, func(prev siteicon.Icon) audit.Entry {
		seen = prev
		return entry(nil, "removed", nil)
	})
	require.NoError(t, err)
	require.True(t, deleted)
	require.Equal(t, "#abcdef", seen.Background)
	require.Equal(t, 1, auditCount(t, q, "removed"))

	_, ok, err := s.Get(ctx)
	require.NoError(t, err)
	require.False(t, ok)
	sizes, err := s.FileSizes(ctx)
	require.NoError(t, err)
	require.Empty(t, sizes, "the files went with the row")
	for _, n := range append([]string{siteicon.Master}, siteicon.Served...) {
		_, _, ok, err := s.File(ctx, n)
		require.NoError(t, err)
		require.False(t, ok, n)
	}

	deleted, err = s.Delete(ctx, func(siteicon.Icon) audit.Entry { return entry(nil, "removed", nil) })
	require.NoError(t, err)
	require.False(t, deleted, "the second delete finds nothing")
	require.Equal(t, 1, auditCount(t, q, "removed"))
}

// The change and its audit entry are one transaction. The entry fails here the
// way the other stores' do: an actor that is no user, which audit_log's foreign
// key refuses. That needs a database that really commits, so each test has one
// of its own.
func TestAtomicWithTheAuditEntry(t *testing.T) {
	ctx := context.Background()
	open := func(t *testing.T) (*siteiconstore.Store, *dbgen.Queries, uuid.UUID) {
		db, closeDB := testutil.NewIsolatedDB(t)
		t.Cleanup(closeDB)
		return siteiconstore.NewWithDB(db.SQL), db.Queries, uuid.New() // a ghost: no such user
	}
	counts := func(t *testing.T, s *siteiconstore.Store) (rows, files int) {
		_, ok, err := s.Get(ctx)
		require.NoError(t, err)
		if ok {
			rows = 1
		}
		sizes, err := s.FileSizes(ctx)
		require.NoError(t, err)
		return rows, len(sizes)
	}

	t.Run("a failed entry stores nothing on the first upload", func(t *testing.T) {
		s, q, ghost := open(t)
		f := fileset("one")
		err := s.Replace(ctx, iconOf(f, "#ffffff"), f, func(*siteicon.Icon) audit.Entry { return entry(&ghost, "uploaded", nil) })
		require.Error(t, err)
		rows, files := counts(t, s)
		require.Zero(t, rows)
		require.Zero(t, files)
		require.Zero(t, auditCount(t, q, "uploaded"))
	})

	t.Run("a failed entry leaves the previous icon as it was", func(t *testing.T) {
		s, q, ghost := open(t)
		one := fileset("one")
		require.NoError(t, s.Replace(ctx, iconOf(one, "#ffffff"), one, func(*siteicon.Icon) audit.Entry { return entry(nil, "uploaded", nil) }))

		two := fileset("two")
		require.Error(t, s.Replace(ctx, iconOf(two, "#000000"), two, func(*siteicon.Icon) audit.Entry { return entry(&ghost, "uploaded", nil) }))
		got, ok, err := s.Get(ctx)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, siteicon.Version(one), got.Version)
		require.Equal(t, "#ffffff", got.Background)
		data, _, _, err := s.File(ctx, "icon-512.png")
		require.NoError(t, err)
		require.Equal(t, one["icon-512.png"], data)
		require.Equal(t, 1, auditCount(t, q, "uploaded"))

		// Update and Delete are the same.
		_, err = s.Update(ctx, func(prev siteicon.Icon, _ siteicon.Files) (siteicon.Icon, siteicon.Files, audit.Entry, error) {
			n := prev
			n.Background, n.Version = "#111111", siteicon.Version(two)
			return n, two, entry(&ghost, "changed", nil), nil
		})
		require.Error(t, err)
		got, _, _ = s.Get(ctx)
		require.Equal(t, siteicon.Version(one), got.Version)

		deleted, err := s.Delete(ctx, func(siteicon.Icon) audit.Entry { return entry(&ghost, "removed", nil) })
		require.Error(t, err)
		require.False(t, deleted)
		rows, files := counts(t, s)
		require.Equal(t, 1, rows)
		require.Equal(t, 7, files, fmt.Sprint("the icon survives a delete whose entry failed"))
	})
}
