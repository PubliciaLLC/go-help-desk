package siteiconstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/siteiconstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/siteicon"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// An upload racing a colour change. A colour change is made from the stored
// files; if it were not under the row's lock, it could carry the OLD master
// across an upload that landed a moment before, and the administrator's new
// picture would be gone with nothing to say so. Each order is forced with a
// call that holds the lock until told to let go, and the other started while it
// does. Real Postgres, on connections that commit.
func TestUpdateAndReplaceSerialiseOnTheRow(t *testing.T) {
	ctx := context.Background()
	open := func(t *testing.T) *siteiconstore.Store {
		db, closeDB := testutil.NewIsolatedDB(t)
		t.Cleanup(closeDB)
		s := siteiconstore.NewWithDB(db.SQL)
		a := fileset("A")
		require.NoError(t, s.Replace(ctx, iconOf(a, "#ffffff"), a, func(*siteicon.Icon) audit.Entry { return entry(nil, "uploaded", nil) }))
		return s
	}
	// recolour is what handleUpdateSiteIcon does: the stored files carried over
	// with a new colour in the four filled ones.
	recolour := func(stored siteicon.Files, bg string) (siteicon.Icon, siteicon.Files) {
		next := siteicon.Files{}
		for k, v := range stored {
			next[k] = v
		}
		next["icon-512.png"] = append([]byte("recoloured "+bg+" "), stored["icon-512.png"]...)
		return iconOf(next, bg), next
	}
	stillRunning := func(done <-chan error) bool {
		select {
		case <-done:
			return false
		case <-time.After(300 * time.Millisecond):
			return true
		}
	}

	t.Run("an upload that arrives while a colour change holds the row waits, and wins", func(t *testing.T) {
		s := open(t)
		inside, release := make(chan struct{}), make(chan struct{})
		upd, rep := make(chan error, 1), make(chan error, 1)

		go func() {
			_, err := s.Update(ctx, func(prev siteicon.Icon, stored siteicon.Files) (siteicon.Icon, siteicon.Files, audit.Entry, error) {
				close(inside)
				<-release
				icon, files := recolour(stored, "#112233")
				return icon, files, entry(nil, "changed", nil), nil
			})
			upd <- err
		}()
		<-inside

		b := fileset("B")
		var prevSeen *siteicon.Icon
		go func() {
			rep <- s.Replace(ctx, iconOf(b, "#112233"), b, func(prev *siteicon.Icon) audit.Entry {
				prevSeen = prev
				return entry(nil, "uploaded", nil)
			})
		}()
		require.True(t, stillRunning(rep), "the upload must wait for the row, not run beside the colour change")

		close(release)
		require.NoError(t, <-upd)
		require.NoError(t, <-rep)

		require.NotNil(t, prevSeen, "the upload's entry was built from what was stored by then")
		require.Equal(t, "#112233", prevSeen.Background, "it saw the colour change, committed")

		got, ok, err := s.Get(ctx)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, siteicon.Version(b), got.Version, "one coherent icon: the upload's")
		for name, want := range b {
			data, _, ok, err := s.File(ctx, name)
			if name == siteicon.Master {
				continue // never served; checked through the sizes below
			}
			require.NoError(t, err)
			require.True(t, ok, name)
			require.Equal(t, want, data, "%s is from the upload, not a mixture", name)
		}
		sizes, err := s.FileSizes(ctx)
		require.NoError(t, err)
		require.Len(t, sizes, 7)
	})

	t.Run("a colour change that arrives while an upload holds the row waits, and starts from the new picture", func(t *testing.T) {
		s := open(t)
		inside, release := make(chan struct{}), make(chan struct{})
		upd, rep := make(chan error, 1), make(chan error, 1)

		b := fileset("B")
		go func() {
			rep <- s.Replace(ctx, iconOf(b, "#ffffff"), b, func(*siteicon.Icon) audit.Entry {
				close(inside)
				<-release
				return entry(nil, "uploaded", nil)
			})
		}()
		<-inside

		var seen siteicon.Files
		go func() {
			_, err := s.Update(ctx, func(prev siteicon.Icon, stored siteicon.Files) (siteicon.Icon, siteicon.Files, audit.Entry, error) {
				seen = stored
				icon, files := recolour(stored, "#445566")
				return icon, files, entry(nil, "changed", nil), nil
			})
			upd <- err
		}()
		require.True(t, stillRunning(upd), "the colour change must wait for the row")

		close(release)
		require.NoError(t, <-rep)
		require.NoError(t, <-upd)

		require.Equal(t, b[siteicon.Master], seen[siteicon.Master], "the colour change was made from the new master, not the one it would have read before")
		got, _, err := s.Get(ctx)
		require.NoError(t, err)
		require.Equal(t, "#445566", got.Background)
		data, _, _, err := s.File(ctx, "favicon-32.png")
		require.NoError(t, err)
		require.Equal(t, b["favicon-32.png"], data, "the transparent files are the upload's")
		data, _, _, err = s.File(ctx, "icon-512.png")
		require.NoError(t, err)
		require.Contains(t, string(data), "recoloured #445566")
		require.Contains(t, string(data), "icon-512.png B", "and the filled one was made from the upload's")
	})
}
