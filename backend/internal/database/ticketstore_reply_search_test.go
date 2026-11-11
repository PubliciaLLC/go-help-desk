package database_test

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/groupstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/replyindex"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// Reply search (advanced search, section 2.5) against a real Postgres: the
// match arm in ListFiltered and the snippet read in ReplyMatches.
//
// SECURITY-CRITICAL. A requester must never match on, or see a snippet of, an
// internal note. If one could match, search would be a yes/no test of what
// staff wrote about them, one word at a time. These tests are the guard's
// proof: each fails if the include_internal condition is removed from either
// query.
//
// The words are made up so a match can only come from the replies written here
// and not from anything another test left committed.
const (
	wordPublic   = "fusilade" // only ever in public replies
	wordInternal = "zanzibar" // only ever in internal notes, except ticket "both"
	wordSubject  = "quillon"  // in a subject, with a decoy internal note beside it
)

type replySearchFixture struct {
	ts        *ticketstore.Store
	cat       category.Category
	status    ticket.Status
	requester user.User
	other     user.User
	staff     user.User

	// All reported by requester unless noted.
	printer ticket.Ticket // public: wordPublic. internal: wordInternal.
	network ticket.Ticket // internal note only: wordInternal.
	laptop  ticket.Ticket // subject: wordSubject. public: nothing. newest internal note: wordSubject + wordInternal.
	split   ticket.Ticket // public: wordPublic only. internal: wordInternal only.
	both    ticket.Ticket // one PUBLIC reply holding wordPublic and wordInternal.
	foreign ticket.Ticket // reported by other: public reply with wordPublic.
}

func newReplySearchFixture(t *testing.T, q *dbgen.Queries) *replySearchFixture {
	t.Helper()
	ctx := context.Background()
	us := userstore.New(q)
	cs := categorystore.New(q)
	ts := ticketstore.New(q)
	f := &replySearchFixture{ts: ts}

	mkUser := func(email string, role user.Role) user.User {
		u := user.User{ID: uuid.New(), Email: email, DisplayName: email, Role: role,
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		require.NoError(t, us.Create(ctx, u))
		return u
	}
	f.requester = mkUser("rs-requester@example.com", user.RoleUser)
	f.other = mkUser("rs-other@example.com", user.RoleUser)
	f.staff = mkUser("rs-staff@example.com", user.RoleStaff)

	f.cat = category.Category{ID: uuid.New(), Name: "ReplySearch", SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, f.cat))
	var err error
	f.status, err = ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)

	base := time.Now().UTC().Truncate(time.Millisecond).Add(-time.Hour)
	tick := 0
	at := func() time.Time { tick++; return base.Add(time.Duration(tick) * time.Minute) }

	mkTicket := func(subject string, reporter uuid.UUID) ticket.Ticket {
		return f.newTicket(t, subject, reporter, at())
	}
	reply := func(tk ticket.Ticket, body string, internal bool) {
		f.newReply(t, tk, body, internal, at())
	}

	f.printer = mkTicket("Printer jam", f.requester.ID)
	reply(f.printer, "The "+wordPublic+" roller squeaks", false)
	reply(f.printer, "Customer reuses "+wordInternal+" toner", true)

	f.network = mkTicket("Network down", f.requester.ID)
	reply(f.network, "The "+wordInternal+" switch is dead", true)

	f.laptop = mkTicket("The "+wordSubject+" laptop", f.requester.ID)
	reply(f.laptop, "Looking into it now", false)
	// Written last, so a leak would pick it as the newest matching reply.
	reply(f.laptop, "decoy "+wordSubject+" "+wordInternal, true)

	f.split = mkTicket("Split words", f.requester.ID)
	reply(f.split, wordPublic+" only", false)
	reply(f.split, wordInternal+" only", true)

	f.both = mkTicket("Both words", f.requester.ID)
	reply(f.both, wordPublic+" and "+wordInternal+" together", false)

	f.foreign = mkTicket("Someone else's", f.other.ID)
	reply(f.foreign, "also a "+wordPublic, false)
	return f
}

func (f *replySearchFixture) newTicket(t *testing.T, subject string, reporter uuid.UUID, at time.Time) ticket.Ticket {
	t.Helper()
	ctx := context.Background()
	seq, err := f.ts.NextSeq(ctx)
	require.NoError(t, err)
	tk := ticket.Ticket{
		ID: uuid.New(), TrackingNumber: ticket.GenerateTrackingNumber(ticket.DefaultTrackingPrefix, 2026, seq),
		Subject: subject, Description: "nothing relevant", DescriptionFormat: ticket.TextFormatPlain,
		SourceChannel: ticket.ChannelWeb, CategoryID: f.cat.ID, Priority: ticket.PriorityMedium, StatusID: f.status.ID,
		ReporterUserID: &reporter, CreatedAt: at, UpdatedAt: at,
	}
	require.NoError(t, f.ts.Create(ctx, tk))
	return tk
}

func (f *replySearchFixture) newReply(t *testing.T, tk ticket.Ticket, body string, internal bool, at time.Time) {
	t.Helper()
	author := f.staff.ID
	require.NoError(t, f.ts.CreateReply(context.Background(), ticket.Reply{
		ID: uuid.New(), TicketID: tk.ID, AuthorID: &author, Body: body, BodyFormat: ticket.TextFormatPlain,
		Internal: internal, NotifyCustomer: !internal, CreatedAt: at,
	}))
}

// result is one search as a viewer sees it: the ids in order, and the snippet
// read for each.
type searchResult struct {
	IDs     []uuid.UUID
	Matches map[uuid.UUID]ticket.ReplyMatch
}

// search runs the two steps of a reply search the way a handler does: list, then
// read the matches for the page, both with the same Filter.
func (f *replySearchFixture) search(t *testing.T, role user.Role, vis ticket.Visibility, actor uuid.UUID, query string, replies bool) searchResult {
	t.Helper()
	ctx := context.Background()
	fl := ticket.Filter{
		ActorID: actor, Visibility: vis, Limit: 50,
		Query: query, SearchReplies: replies,
		// From the role and nothing else: the same function every route uses.
		IncludeInternal: ticket.IncludeInternalFor(role),
	}
	got, err := f.ts.ListFiltered(ctx, fl)
	require.NoError(t, err)
	res := searchResult{}
	for _, tk := range got {
		res.IDs = append(res.IDs, tk.ID)
	}
	res.Matches, err = f.ts.ReplyMatches(ctx, fl, res.IDs)
	require.NoError(t, err)
	return res
}

func (f *replySearchFixture) asRequester(t *testing.T, query string) searchResult {
	return f.search(t, user.RoleUser, ticket.VisibilityReporter, f.requester.ID, query, true)
}

func (f *replySearchFixture) asStaff(t *testing.T, query string) searchResult {
	return f.search(t, user.RoleStaff, ticket.VisibilityAll, f.staff.ID, query, true)
}

func has(ids []uuid.UUID, tk ticket.Ticket) bool {
	for _, id := range ids {
		if id == tk.ID {
			return true
		}
	}
	return false
}

// A ticket whose only match is an internal note is absent for a requester and
// present for staff.
func TestReplySearch_OnlyAnInternalNoteMatches(t *testing.T) {
	testutil.EnsureReplySearchReady(t)
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	f := newReplySearchFixture(t, q)

	req := f.asRequester(t, wordInternal)
	require.False(t, has(req.IDs, f.network), "a requester must not find a ticket through an internal note")
	require.False(t, has(req.IDs, f.printer), "nor one that also has a public reply about something else")
	require.False(t, has(req.IDs, f.laptop))
	require.False(t, has(req.IDs, f.split))
	require.True(t, has(req.IDs, f.both), "the public reply that holds the word still matches")
	require.Len(t, req.IDs, 1)

	staff := f.asStaff(t, wordInternal)
	require.True(t, has(staff.IDs, f.network))
	require.True(t, has(staff.IDs, f.printer))
	require.True(t, has(staff.IDs, f.laptop))
	require.True(t, has(staff.IDs, f.split))
	require.True(t, has(staff.IDs, f.both))
}

// Replies are searched only when asked for, whoever is asking.
func TestReplySearch_OffUnlessAskedFor(t *testing.T) {
	testutil.EnsureReplySearchReady(t)
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	f := newReplySearchFixture(t, q)

	for _, role := range []user.Role{user.RoleUser, user.RoleStaff, user.RoleAdmin} {
		got := f.search(t, role, ticket.VisibilityAll, f.staff.ID, wordPublic, false)
		require.False(t, has(got.IDs, f.printer), "%s: no reply search without search_replies", role)
		require.Empty(t, got.Matches)
	}
}

// A ticket that matches on its own subject must not show a snippet from an
// internal note to a requester, even when that note is the newest matching reply.
func TestReplySearch_NoSnippetFromAnInternalNote(t *testing.T) {
	testutil.EnsureReplySearchReady(t)
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	f := newReplySearchFixture(t, q)

	req := f.asRequester(t, wordSubject)
	require.True(t, has(req.IDs, f.laptop), "it matches on its subject")
	require.NotContains(t, req.Matches, f.laptop.ID, "but no snippet may come from the internal note")

	staff := f.asStaff(t, wordSubject)
	m, ok := staff.Matches[f.laptop.ID]
	require.True(t, ok, "staff do get the internal note as the match")
	require.True(t, m.Internal)
	require.Contains(t, m.Snippet, ticket.SnippetStart+wordSubject+ticket.SnippetStop)
}

// The words of a search must all be in ONE reply. A public reply and an internal
// note cannot combine into a match.
func TestReplySearch_WordsMustShareOneReply(t *testing.T) {
	testutil.EnsureReplySearchReady(t)
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	f := newReplySearchFixture(t, q)

	q2 := wordPublic + " " + wordInternal
	req := f.asRequester(t, q2)
	require.False(t, has(req.IDs, f.split), "public + internal must not combine for a requester")
	require.False(t, has(req.IDs, f.printer))
	require.True(t, has(req.IDs, f.both), "both words in one public reply is a match")
	require.Len(t, req.IDs, 1)

	staff := f.asStaff(t, q2)
	require.False(t, has(staff.IDs, f.split), "nor for staff: the rule is one reply, not who may read it")
	require.False(t, has(staff.IDs, f.printer))
	require.True(t, has(staff.IDs, f.both))
}

// Visibility still applies: a reply hit cannot bring in a ticket the viewer may
// not see.
func TestReplySearch_ReplyHitDoesNotWidenVisibility(t *testing.T) {
	testutil.EnsureReplySearchReady(t)
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	f := newReplySearchFixture(t, q)

	req := f.asRequester(t, wordPublic)
	require.False(t, has(req.IDs, f.foreign), "someone else's ticket, whose reply holds the word")
	require.True(t, has(req.IDs, f.printer))
	require.NotContains(t, req.Matches, f.foreign.ID)

	// The snippet read is a second door: handed the id of a ticket the viewer
	// does not own, it still reads only public replies (the caller passes the
	// ids of a page it listed, and this is what holds if one slips in).
	fl := ticket.Filter{Query: wordPublic, SearchReplies: true, IncludeInternal: false}
	got, err := f.ts.ReplyMatches(context.Background(), fl, []uuid.UUID{f.network.ID})
	require.NoError(t, err)
	require.Empty(t, got, "an internal-only ticket yields nothing when internal notes are off")
}

// Matches and snippets: the newest matching reply, marked with control
// characters, no author, cut to length.
func TestReplyMatches_Snippet(t *testing.T) {
	testutil.EnsureReplySearchReady(t)
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	f := newReplySearchFixture(t, q)
	ctx := context.Background()

	got := f.asRequester(t, wordPublic)
	m, ok := got.Matches[f.printer.ID]
	require.True(t, ok)
	require.False(t, m.Internal)
	require.Contains(t, m.Snippet, ticket.SnippetStart+wordPublic+ticket.SnippetStop)
	require.NotEqual(t, uuid.Nil, m.ReplyID)
	require.False(t, m.CreatedAt.IsZero())
	require.NotContains(t, m.Snippet, "<", "the server produces no markup")

	t.Run("a long reply is cut and the match is balanced", func(t *testing.T) {
		long := ticket.Reply{
			ID: uuid.New(), TicketID: f.printer.ID, AuthorID: &f.staff.ID, BodyFormat: ticket.TextFormatPlain,
			NotifyCustomer: true, CreatedAt: time.Now().UTC().Add(time.Hour),
		}
		for i := 0; i < 400; i++ {
			long.Body += "<b>padding</b> "
		}
		long.Body += wordPublic
		require.NoError(t, f.ts.CreateReply(ctx, long))
		got, err := f.ts.ReplyMatches(ctx, ticket.Filter{Query: wordPublic, SearchReplies: true}, []uuid.UUID{f.printer.ID})
		require.NoError(t, err)
		m := got[f.printer.ID]
		require.Equal(t, long.ID, m.ReplyID, "the newest matching reply")
		require.LessOrEqual(t, len([]rune(m.Snippet)), ticket.MaxSnippetRunes)
		require.Equal(t, countRune(m.Snippet, '\x02'), countRune(m.Snippet, '\x03'), "every highlight is closed")
	})

	t.Run("nothing to search for", func(t *testing.T) {
		got, err := f.ts.ReplyMatches(ctx, ticket.Filter{Query: "???", SearchReplies: true, IncludeInternal: true}, []uuid.UUID{f.printer.ID})
		require.NoError(t, err)
		require.Empty(t, got, "a query with no words matches no reply")
		got, err = f.ts.ReplyMatches(ctx, ticket.Filter{Query: wordPublic, IncludeInternal: true}, []uuid.UUID{f.printer.ID})
		require.NoError(t, err)
		require.Empty(t, got, "no reply search unless asked for")
		got, err = f.ts.ReplyMatches(ctx, ticket.Filter{Query: wordPublic, SearchReplies: true}, nil)
		require.NoError(t, err)
		require.Empty(t, got)
	})
}

func countRune(s string, r rune) int {
	n := 0
	for _, c := range s {
		if c == r {
			n++
		}
	}
	return n
}

// Ranking: a ticket that matches on its own subject or description comes before
// one that matches only through a reply.
func TestReplySearch_TicketTextOutranksReplyOnly(t *testing.T) {
	testutil.EnsureReplySearchReady(t)
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	f := newReplySearchFixture(t, q)

	got := f.asStaff(t, wordSubject)
	require.GreaterOrEqual(t, len(got.IDs), 1)
	require.Equal(t, f.laptop.ID, got.IDs[0])

	// A ticket that matches only through a reply and was created AFTER the direct
	// match: newest-first alone would put it first. It stays second.
	late := f.newTicket(t, "Unrelated subject", f.requester.ID, time.Now().UTC().Add(time.Hour))
	f.newReply(t, late, "mentions "+wordSubject, false, time.Now().UTC().Add(time.Hour))
	got = f.asStaff(t, wordSubject)
	require.True(t, has(got.IDs, late))
	require.Equal(t, f.laptop.ID, got.IDs[0], "a direct match outranks a reply-only match")
	require.Equal(t, late.ID, got.IDs[1])
}

// THE INVARIANT (design 2.5, rule 4). Run a set of searches as a requester, then
// delete every internal note and run them again. The two results must be
// identical: ids, order and snippets. This fails for any way an internal note
// influences a requester's results, including ones nobody has thought of yet.
func TestReplySearch_RequesterResultsDoNotDependOnInternalNotes(t *testing.T) {
	testutil.EnsureReplySearchReady(t)
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	defer rollback()
	f := newReplySearchFixture(t, q)

	queries := []string{
		wordPublic, wordInternal, wordSubject,
		wordPublic + " " + wordInternal,
		wordSubject + " " + wordInternal,
		"zanz", "FUSILADE", "squeaks", "decoy", "toner", "switch", "customer", "nothing",
	}
	run := func() map[string]searchResult {
		out := map[string]searchResult{}
		for _, query := range queries {
			out[query] = f.asRequester(t, query)
		}
		return out
	}

	before := run()

	// Not vacuous: staff do see the notes, and there are notes to delete.
	var internalNotes int
	require.NoError(t, tx.QueryRowContext(context.Background(), `SELECT count(*) FROM ticket_replies WHERE internal`).Scan(&internalNotes))
	require.Greater(t, internalNotes, 0)
	differs := false
	for _, query := range queries {
		s := f.asStaff(t, query)
		if len(s.IDs) != len(before[query].IDs) || len(s.Matches) != len(before[query].Matches) {
			differs = true
		}
	}
	require.True(t, differs, "the fixture must give staff results a requester does not get, or this test proves nothing")

	deleteInternalNotes(t, tx)
	after := run()
	require.Equal(t, before, after, "a requester's search results changed when the internal notes were deleted")
}

// planRecorder passes every query through to the transaction and keeps the plan
// Postgres chose for it, so a test can look at the plan of the real statement
// the store runs and not at a copy of its text.
type planRecorder struct {
	dbgen.DBTX
	tx    *sql.Tx
	plans []string
}

func (p *planRecorder) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	rows, err := p.tx.QueryContext(ctx, "EXPLAIN "+query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var plan string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return nil, err
		}
		plan += line + "\n"
	}
	p.plans = append(p.plans, plan)
	return p.tx.QueryContext(ctx, query, args...)
}

// The match query has to be able to use both of its indexes: the content index
// for a rare word, and the recency index to walk replies newest first for a
// common one. A table this small is never planned that way on its own, so each
// test takes the alternatives away (in a transaction that rolls back, in a
// database of the test's own) and looks at what is left. If the query stopped
// matching the column, or stopped ordering by created_at, the plan would say
// Seq Scan.
func TestReplySearch_MatchQueryCanUseItsIndexes(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	ctx := context.Background()
	prep := replyindex.New(db.SQL, slog.New(slog.DiscardHandler))
	prep.Pause = 0
	_, err := prep.Prepare(ctx) // an empty table: just builds the indexes
	require.NoError(t, err)

	for _, tc := range []struct {
		name  string
		off   []string
		index string
	}{
		{"content index", []string{"enable_seqscan", "enable_indexscan"}, "ticket_replies_body_tsv_idx"},
		{"recency index", []string{"enable_seqscan", "enable_bitmapscan"}, "ticket_replies_created_at_idx"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, tx, rollback := testutil.TxQueriesTx(t, db)
			defer rollback()
			f := newReplySearchFixture(t, dbgen.New(tx))
			for _, setting := range tc.off {
				_, err := tx.ExecContext(ctx, `SET LOCAL `+setting+` = off`)
				require.NoError(t, err)
			}
			_, err := tx.ExecContext(ctx, `ANALYZE ticket_replies`)
			require.NoError(t, err)

			rec := &planRecorder{DBTX: tx, tx: tx}
			// A long limit: with the alternatives switched off the plan can be slow,
			// and a statement cancelled by the limit would end this transaction.
			ts := ticketstore.New(dbgen.New(rec)).WithReplyLimits(ticketstore.DefaultReplyCap, time.Minute)
			res, err := ts.ListFilteredWithMeta(ctx, ticket.Filter{
				ActorID: f.staff.ID, Visibility: ticket.VisibilityAll, Limit: 50,
				Query: wordPublic, SearchReplies: true, IncludeInternal: true,
			})
			require.NoError(t, err)
			requireSearched(t, res.Replies)
			// The first statement recorded is the readiness check; the list is the last.
			plan := rec.plans[len(rec.plans)-1]
			require.Contains(t, plan, tc.index, "plan:\n%s", plan)
		})
	}
}

// Until the background fill and the index build are done, a search that asks
// for replies skips them and says so: it answers with the tickets that match
// on their own text, never with a scan of every reply, and never with a
// half-filled column's partial answer. Once ready, the same search finds the
// reply-only ticket.
func TestReplySearch_SkippedWhilePreparing(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t) // no indexes yet
	defer closeDB()
	ctx := context.Background()

	_, tx, rollback := testutil.TxQueriesTx(t, db)
	// Also when an assertion below fails and ends the test here: closing the
	// isolated database waits for an open transaction, and the test would hang.
	// (rollback is called again before the second half; that is harmless.)
	defer rollback()
	f := newReplySearchFixture(t, dbgen.New(tx))
	ts := ticketstore.New(dbgen.New(tx))
	fl := ticket.Filter{ActorID: f.staff.ID, Visibility: ticket.VisibilityAll, Limit: 50, Query: wordSubject, SearchReplies: true, IncludeInternal: true}

	res, err := ts.ListFilteredWithMeta(ctx, fl)
	require.NoError(t, err)
	requirePreparing(t, res.Replies)
	require.False(t, res.Replies.ExcerptsApply())
	var ids []uuid.UUID
	for _, tk := range res.Tickets {
		ids = append(ids, tk.ID)
	}
	require.True(t, has(ids, f.laptop), "the subject match is still found")

	// A word that only a reply holds finds nothing yet, not an error.
	fl.Query = wordPublic
	res, err = ts.ListFilteredWithMeta(ctx, fl)
	require.NoError(t, err)
	requirePreparing(t, res.Replies)
	require.Empty(t, res.Tickets)
	rollback()

	prep := replyindex.New(db.SQL, slog.New(slog.DiscardHandler))
	prep.Pause = 0
	_, err = prep.Prepare(ctx)
	require.NoError(t, err)

	_, tx, rollback = testutil.TxQueriesTx(t, db)
	defer rollback()
	f = newReplySearchFixture(t, dbgen.New(tx))
	ts = ticketstore.New(dbgen.New(tx))
	fl.ActorID = f.staff.ID
	res, err = ts.ListFilteredWithMeta(ctx, fl)
	require.NoError(t, err)
	requireSearched(t, res.Replies)
	require.True(t, res.Replies.ExcerptsApply())
	require.NotEmpty(t, res.Tickets, "the same search finds the reply-only ticket once ready")
}

// A search that asks for replies and runs out of time answers without them and
// says so, instead of failing or holding the request.
func TestReplySearch_TimeLimitDropsTheReplyArm(t *testing.T) {
	testutil.EnsureReplySearchReady(t)
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	f := newReplySearchFixture(t, q)
	ctx := context.Background()

	fl := ticket.Filter{ActorID: f.staff.ID, Visibility: ticket.VisibilityAll, Limit: 50, Query: wordSubject, SearchReplies: true, IncludeInternal: true}
	// A limit that has passed before the statement starts.
	res, err := ticketstore.New(q).WithReplyLimits(ticketstore.DefaultReplyCap, time.Nanosecond).ListFilteredWithMeta(ctx, fl)
	require.NoError(t, err)
	requireTimedOut(t, res.Replies)
	require.False(t, res.Replies.ExcerptsApply(), "no excerpts for a page chosen without replies")
	var ids []uuid.UUID
	for _, tk := range res.Tickets {
		ids = append(ids, tk.ID)
	}
	require.True(t, has(ids, f.laptop), "the tickets that match on their own text are still returned")

	// The caller's own cancelled context is the caller's, not a time limit.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = ticketstore.New(q).ListFilteredWithMeta(cancelled, fl)
	require.Error(t, err)

	// Not asked for: nothing to flag.
	fl.SearchReplies = false
	res, err = ticketstore.New(q).WithReplyLimits(ticketstore.DefaultReplyCap, time.Nanosecond).ListFilteredWithMeta(ctx, fl)
	require.NoError(t, err)
	require.Equal(t, ticket.ReplySearchMeta{}, res.Replies, "replies not asked for: nothing to say")
}

// The cap on reply matches is applied AFTER the viewer's visibility. If it were
// applied before, a requester's own matches would be crowded out by everyone
// else's newer ones, and a word common across the help desk would find nothing
// in their own tickets.
func TestReplySearch_CapIsAppliedToWhatTheViewerMaySee(t *testing.T) {
	testutil.EnsureReplySearchReady(t)
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	f := newReplySearchFixture(t, q)
	ts := ticketstore.New(q).WithReplyLimits(1, ticketstore.DefaultReplyTimeout)

	// wordPublic is in three public replies: printer's, both's, and (newest of
	// all) the other requester's. A cap of one keeps only the newest the viewer
	// may see.
	run := func(role user.Role, vis ticket.Visibility, actor uuid.UUID) map[uuid.UUID]bool {
		fl := ticket.Filter{ActorID: actor, Visibility: vis, Limit: 50, Query: wordPublic, SearchReplies: true,
			IncludeInternal: ticket.IncludeInternalFor(role)}
		res, err := ts.ListFilteredWithMeta(context.Background(), fl)
		require.NoError(t, err)
		requireSearched(t, res.Replies)
		got := map[uuid.UUID]bool{}
		for _, tk := range res.Tickets {
			got[tk.ID] = true
		}
		return got
	}

	all := run(user.RoleStaff, ticket.VisibilityAll, f.staff.ID)
	require.Equal(t, map[uuid.UUID]bool{f.foreign.ID: true}, all, "an administrator's newest match is the other requester's ticket")

	mine := run(user.RoleUser, ticket.VisibilityReporter, f.requester.ID)
	require.Equal(t, map[uuid.UUID]bool{f.both.ID: true}, mine,
		"the requester's newest match, not nothing because newer matches belong to someone else")

	scoped := run(user.RoleStaff, ticket.VisibilityScoped, f.requester.ID)
	require.Equal(t, map[uuid.UUID]bool{f.both.ID: true}, scoped, "the same for the staff scope")
}

func deleteInternalNotes(t *testing.T, tx *sql.Tx) {
	t.Helper()
	res, err := tx.ExecContext(context.Background(), `DELETE FROM ticket_replies WHERE internal`)
	require.NoError(t, err)
	n, err := res.RowsAffected()
	require.NoError(t, err)
	require.Greater(t, n, int64(0))
}

// What the store says about a reply search, in ticket.ReplySearchMeta (the REST
// handler turns it into X-Replies-Searched and X-Reply-Index).
func requireSearched(t *testing.T, m ticket.ReplySearchMeta) {
	t.Helper()
	require.NotNil(t, m.Searched)
	require.True(t, *m.Searched)
	require.False(t, m.IndexBuilding)
}

func requirePreparing(t *testing.T, m ticket.ReplySearchMeta) {
	t.Helper()
	require.NotNil(t, m.Searched)
	require.False(t, *m.Searched, "not searched")
	require.True(t, m.IndexBuilding, "because the index is still being built")
}

func requireTimedOut(t *testing.T, m ticket.ReplySearchMeta) {
	t.Helper()
	require.NotNil(t, m.Searched)
	require.False(t, *m.Searched, "not searched")
	require.False(t, m.IndexBuilding, "the index is ready; the search was too slow")
}

// The reply arm repeats the scoped-staff rule of the main query (as the same
// arrays: groups, whole categories, one type of a category). If the two drifted,
// a staff member's search would find replies of tickets they may not see, or miss
// ones they may. The main query still filters by the rule, so the first can only
// show up as a wasted cap; the second is a missed ticket, and this is what holds
// it: every kind of scope, and what lies outside each.
func TestReplySearch_ScopedStaffFindsExactlyWhatTheirScopeAdmits(t *testing.T) {
	testutil.EnsureReplySearchReady(t)
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	f := newReplySearchFixture(t, q)
	ctx := context.Background()
	cs := categorystore.New(q)
	gs := groupstore.New(q)

	member := user.User{ID: uuid.New(), Email: "rs-scoped@example.com", DisplayName: "scoped", Role: user.RoleStaff,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	require.NoError(t, userstore.New(q).Create(ctx, member))
	catX := category.Category{ID: uuid.New(), Name: "RSX", SortOrder: 2, Active: true}
	catY := category.Category{ID: uuid.New(), Name: "RSY", SortOrder: 3, Active: true}
	typeY1 := category.Type{ID: uuid.New(), CategoryID: catY.ID, Name: "Y1", SortOrder: 1, Active: true}
	typeY2 := category.Type{ID: uuid.New(), CategoryID: catY.ID, Name: "Y2", SortOrder: 2, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, catX))
	require.NoError(t, cs.CreateCategory(ctx, catY))
	require.NoError(t, cs.CreateType(ctx, typeY1))
	require.NoError(t, cs.CreateType(ctx, typeY2))
	g := group.Group{ID: uuid.New(), Name: "RSGroup"}
	require.NoError(t, gs.Create(ctx, g))
	require.NoError(t, gs.AddMember(ctx, g.ID, member.ID))
	require.NoError(t, gs.AddScope(ctx, group.GroupScope{GroupID: g.ID, CategoryID: catX.ID}))                     // all of X
	require.NoError(t, gs.AddScope(ctx, group.GroupScope{GroupID: g.ID, CategoryID: catY.ID, TypeID: &typeY1.ID})) // one type of Y

	const word = "gallimaufry"
	n := 0
	mk := func(subject string, mutate func(*ticket.Ticket)) uuid.UUID {
		n++
		at := time.Now().UTC().Add(time.Duration(n) * time.Second)
		seq, err := f.ts.NextSeq(ctx)
		require.NoError(t, err)
		reporter := f.other.ID
		tk := ticket.Ticket{
			ID: uuid.New(), TrackingNumber: ticket.GenerateTrackingNumber(ticket.DefaultTrackingPrefix, 2026, seq),
			Subject: subject, Description: "nothing relevant", DescriptionFormat: ticket.TextFormatPlain,
			SourceChannel: ticket.ChannelWeb, CategoryID: f.cat.ID, Priority: ticket.PriorityMedium, StatusID: f.status.ID,
			ReporterUserID: &reporter, CreatedAt: at, UpdatedAt: at,
		}
		mutate(&tk)
		require.NoError(t, f.ts.Create(ctx, tk))
		f.newReply(t, tk, "about the "+word, false, at)
		return tk.ID
	}
	in := map[uuid.UUID]bool{
		mk("whole category", func(tk *ticket.Ticket) { tk.CategoryID = catX.ID }):                          true,
		mk("the covered type", func(tk *ticket.Ticket) { tk.CategoryID, tk.TypeID = catY.ID, &typeY1.ID }): true,
		mk("assigned to the group", func(tk *ticket.Ticket) { tk.AssigneeGroupID = &g.ID }):                true,
		mk("assigned to them", func(tk *ticket.Ticket) { tk.AssigneeUserID = &member.ID }):                 true,
		mk("reported by them", func(tk *ticket.Ticket) { tk.ReporterUserID = &member.ID }):                 true,
		mk("the other type", func(tk *ticket.Ticket) { tk.CategoryID, tk.TypeID = catY.ID, &typeY2.ID }):   false,
		mk("typeless in the typed category", func(tk *ticket.Ticket) { tk.CategoryID = catY.ID }):          false,
		mk("nobody's", func(tk *ticket.Ticket) {}):                                                         false,
	}

	res, err := f.ts.ListFilteredWithMeta(ctx, ticket.Filter{
		ActorID: member.ID, Visibility: ticket.VisibilityScoped, Limit: 50,
		Query: word, SearchReplies: true, IncludeInternal: true,
	})
	require.NoError(t, err)
	requireSearched(t, res.Replies)
	got := map[uuid.UUID]bool{}
	for _, tk := range res.Tickets {
		got[tk.ID] = true
	}
	for id, want := range in {
		require.Equal(t, want, got[id], "ticket %s", id)
	}
	require.Len(t, got, 5)
}

// A reply is searched, and excerpted, on its first 50,000 characters (migration
// 000062): 1.2.0 allowed replies of any length, and a megabyte of distinct words
// cannot be a tsvector. A word near the start of a very long reply is found and
// excerpted; one past the limit is not found.
func TestReplySearch_ALongReplyIsSearchedOnItsFirst50000Characters(t *testing.T) {
	testutil.EnsureReplySearchReady(t)
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	f := newReplySearchFixture(t, q)
	ctx := context.Background()

	const early, late = "paleontologist", "xylophonist"
	long := early + " noted in the log. " + strings.Repeat("filler words go here. ", 5000) + late + " appears at the very end."
	require.Greater(t, len(long), 50000)
	tk := f.newTicket(t, "A very long reply", f.requester.ID, time.Now().UTC())
	f.newReply(t, tk, long, false, time.Now().UTC())

	fl := ticket.Filter{ActorID: f.staff.ID, Visibility: ticket.VisibilityAll, Limit: 50, Query: early, SearchReplies: true, IncludeInternal: true}
	res, err := f.ts.ListFilteredWithMeta(ctx, fl)
	require.NoError(t, err)
	requireSearched(t, res.Replies)
	var ids []uuid.UUID
	for _, x := range res.Tickets {
		ids = append(ids, x.ID)
	}
	require.True(t, has(ids, tk), "a word near the start is found")
	matches, err := f.ts.ReplyMatches(ctx, fl, ids)
	require.NoError(t, err)
	require.Contains(t, matches[tk.ID].Snippet, "\u0002"+early+"\u0003", "and excerpted")

	fl.Query = late
	res, err = f.ts.ListFilteredWithMeta(ctx, fl)
	require.NoError(t, err)
	require.Empty(t, res.Tickets, "a word past the first 50,000 characters is not found")
}
