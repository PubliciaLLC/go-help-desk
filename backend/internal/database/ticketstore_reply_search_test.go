package database_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
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

// The match query has to be able to use ticket_replies_body_fts_idx. The index
// is an expression index, so the planner can use it only if the query spells the
// expression exactly as the migration does; a query that drifted to another
// spelling would still return the right rows, by scanning every reply.
//
// On a table this small the planner would never choose it, so the test takes
// away the alternatives: sequential scans are off and ticket_replies_ticket_idx
// is dropped (in a transaction that rolls back, in a database of this test's
// own, because DROP INDEX locks the table). What is left is the GIN index, or a
// plan that says Seq Scan.
func TestReplySearch_MatchQueryUsesTheReplyIndex(t *testing.T) {
	db, closeDB := testutil.NewIsolatedDB(t)
	defer closeDB()
	_, tx, rollback := testutil.TxQueriesTx(t, db)
	defer rollback()
	f := newReplySearchFixture(t, dbgen.New(tx))
	ctx := context.Background()
	for _, stmt := range []string{`DROP INDEX ticket_replies_ticket_idx`, `SET LOCAL enable_seqscan = off`, `ANALYZE ticket_replies`} {
		_, err := tx.ExecContext(ctx, stmt)
		require.NoError(t, err, stmt)
	}

	rec := &planRecorder{DBTX: tx, tx: tx}
	ts := ticketstore.New(dbgen.New(rec))
	_, err := ts.ListFiltered(ctx, ticket.Filter{
		ActorID: f.staff.ID, Visibility: ticket.VisibilityAll, Limit: 50,
		Query: wordPublic, SearchReplies: true, IncludeInternal: true,
	})
	require.NoError(t, err)
	require.Len(t, rec.plans, 1)
	require.Contains(t, rec.plans[0], "ticket_replies_body_fts_idx", "plan:\n%s", rec.plans[0])
}

func deleteInternalNotes(t *testing.T, tx *sql.Tx) {
	t.Helper()
	res, err := tx.ExecContext(context.Background(), `DELETE FROM ticket_replies WHERE internal`)
	require.NoError(t, err)
	n, err := res.RowsAffected()
	require.NoError(t, err)
	require.Greater(t, n, int64(0))
}
