package database_test

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// awaiting_reply (#675): the ticket is not Resolved or Closed and the newest
// public reply is the requester's side. Tested against Postgres, one ticket per
// case, so a wrong answer names the case.
func TestListFiltered_AwaitingReply(t *testing.T) {
	f := newSearchFixture(t)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	admin := user.User{ID: uuid.New(), Email: "ada.admin@example.com", DisplayName: "Ada Admin", Role: user.RoleAdmin,
		CreatedAt: now, UpdatedAt: now}
	require.NoError(t, f.us.Create(f.ctx, admin))
	gone := user.User{ID: uuid.New(), Email: "gone.staff@example.com", DisplayName: "Gone Staff", Role: user.RoleStaff,
		CreatedAt: now, UpdatedAt: now}
	require.NoError(t, f.us.Create(f.ctx, gone))
	require.NoError(t, f.us.SoftDelete(f.ctx, gone.ID))

	step := 0
	reply := func(tk ticket.Ticket, author *user.User, internal bool) {
		step++
		r := ticket.Reply{ID: uuid.New(), TicketID: tk.ID, Body: "b", BodyFormat: ticket.TextFormatPlain,
			Internal: internal, CreatedAt: now.Add(time.Duration(step) * time.Minute)}
		if author != nil {
			r.AuthorID = &author.ID
		}
		require.NoError(t, f.ts.CreateReply(f.ctx, r))
	}

	cases := []struct {
		name   string
		status *ticket.Status
		thread func(tk ticket.Ticket)
		want   bool
	}{
		{"no reply: the description is the requester's message", nil, func(ticket.Ticket) {}, true},
		{"requester last", nil, func(tk ticket.Ticket) { reply(tk, &f.staff, false); reply(tk, &f.bob, false) }, true},
		{"staff last", nil, func(tk ticket.Ticket) { reply(tk, &f.bob, false); reply(tk, &f.staff, false) }, false},
		{"internal note after the requester: still waiting", nil, func(tk ticket.Ticket) { reply(tk, &f.bob, false); reply(tk, &f.staff, true) }, true},
		{"internal note after staff: not waiting", nil, func(tk ticket.Ticket) { reply(tk, &f.staff, false); reply(tk, &f.bob, true) }, false},
		{"only an internal note", nil, func(tk ticket.Ticket) { reply(tk, &f.staff, true) }, true},
		{"guest (no author) last", nil, func(tk ticket.Ticket) { reply(tk, &f.staff, false); reply(tk, nil, false) }, true},
		{"deleted staff member last: still the help desk's", nil, func(tk ticket.Ticket) { reply(tk, &f.bob, false); reply(tk, &gone, false) }, false},
		{"administrator last", nil, func(tk ticket.Ticket) { reply(tk, &f.bob, false); reply(tk, &admin, false) }, false},
		{"requester after the administrator", nil, func(tk ticket.Ticket) { reply(tk, &admin, false); reply(tk, &f.bob, false) }, true},
		{"pending, staff asked", &f.stPending, func(tk ticket.Ticket) { reply(tk, &f.staff, false) }, false},
		{"pending, requester answered", &f.stPending, func(tk ticket.Ticket) { reply(tk, &f.staff, false); reply(tk, &f.bob, false) }, true},
		{"resolved, requester last: never", &f.stResolved, func(tk ticket.Ticket) { reply(tk, &f.bob, false) }, false},
		{"closed, requester last: never", &f.stClosed, func(tk ticket.Ticket) { reply(tk, &f.bob, false) }, false},
		{"closed, no reply: never", &f.stClosed, func(ticket.Ticket) {}, false},
	}
	var want []string
	for _, tc := range cases {
		tk := f.mk(spec{subject: "aw " + tc.name, reporter: &f.bob, status: tc.status, created: now.Add(-time.Hour)})
		tc.thread(tk)
		if tc.want {
			want = append(want, tk.Subject)
		}
	}
	slices.Sort(want)
	require.NotEmpty(t, want)

	// Only the tickets above, so the fixture's own tickets do not count.
	mine := func(fl ticket.Filter) ticket.Filter {
		fl.CreatedAfter = ref(now.Add(-2 * time.Hour))
		fl.Limit = 1000
		return fl
	}
	viewers := []struct {
		name string
		base ticket.Filter
	}{
		{"admin", ticket.Filter{ActorID: admin.ID, Visibility: ticket.VisibilityAll}},
		{"staff", ticket.Filter{ActorID: f.staff.ID, Visibility: ticket.VisibilityAll}},
		// One meaning for every caller; the role gate is AllowedFor, not the store.
		{"reporter visibility", ticket.Filter{ActorID: f.bob.ID, Visibility: ticket.VisibilityReporter}},
	}
	for _, v := range viewers {
		t.Run(v.name, func(t *testing.T) {
			fl := mine(v.base)
			fl.AwaitingReply = true
			got, err := f.ts.ListFilteredAt(f.ctx, fl, now)
			require.NoError(t, err)
			var names []string
			for _, tk := range got {
				names = append(names, tk.Subject)
			}
			slices.Sort(names)
			require.Equal(t, want, names)

			n, err := f.ts.CountFilteredAt(f.ctx, fl, now)
			require.NoError(t, err)
			require.EqualValues(t, len(want), n, "count must equal the list")

			// false is the same as not asking.
			off := mine(v.base)
			all, err := f.ts.ListFilteredAt(f.ctx, off, now)
			require.NoError(t, err)
			require.Len(t, all, len(cases))
		})
	}
}

// ReopenedAfter is inclusive, a never-reopened ticket has no instant to test,
// and nil is not filtered.
func TestListFiltered_ReopenedAfter(t *testing.T) {
	f := newSearchFixture(t)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	cut := now.Add(-7 * 24 * time.Hour)

	reopen := func(subject string, at *time.Time) {
		tk := f.mk(spec{subject: subject, created: now.Add(-30 * 24 * time.Hour)})
		if at != nil {
			_, err := f.tx.ExecContext(f.ctx, `UPDATE tickets SET last_reopened_at = $1 WHERE id = $2`, *at, tk.ID)
			require.NoError(t, err)
		}
	}
	reopen("ro at the boundary", &cut)
	reopen("ro one microsecond before", ref(cut.Add(-time.Microsecond)))
	reopen("ro after", ref(cut.Add(time.Hour)))
	reopen("ro never", nil)

	fl := f.all()
	fl.Query = "ro"
	fl.SearchReplies = false
	fl.ReopenedAfter = &cut
	require.Equal(t, []string{"ro after", "ro at the boundary"}, f.set(fl))
	n, err := f.ts.CountFilteredAt(f.ctx, fl, now)
	require.NoError(t, err)
	require.EqualValues(t, 2, n)

	fl.ReopenedAfter = nil
	require.Len(t, f.set(fl), 4)
}
