package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// Reply search over REST (advanced search, section 2.5): GET /tickets with
// search_replies=true carries a reply_match on each ticket found through a
// reply, with the same rules as MCP list_tickets.
//
// SECURITY-CRITICAL: a requester, or a requester's key, must never match on or
// see an excerpt of an internal note. The role decides which replies are
// searched; no request parameter does.

const restWordSubject = "quillon" // in a subject, with a decoy internal note beside it

type restListed struct {
	ID         uuid.UUID `json:"id"`
	ReplyMatch *struct {
		ReplyID   uuid.UUID `json:"reply_id"`
		Internal  bool      `json:"internal"`
		CreatedAt time.Time `json:"created_at"`
		Snippet   string    `json:"snippet"`
	} `json:"reply_match"`
}

// restSearch runs the search as one of the harness callers and returns the raw
// body as well as the decoded list.
func restSearch(t *testing.T, h *harness, as func(*testing.T, string, string, any) *http.Response, q string, extra url.Values) ([]restListed, string) {
	t.Helper()
	v := url.Values{"q": {q}, "search_replies": {"true"}, "scope": {"visible"}} // every ticket the caller may see
	for k, vals := range extra {
		v[k] = vals
	}
	res := as(t, http.MethodGet, "/api/v1/tickets?"+v.Encode(), nil)
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))
	var out []restListed
	require.NoError(t, json.Unmarshal(body, &out), string(body))
	return out, string(body)
}

func restIDs(l []restListed) map[uuid.UUID]*restListed {
	m := map[uuid.UUID]*restListed{}
	for i := range l {
		m[l[i].ID] = &l[i]
	}
	return m
}

// laptop is the ticket whose SUBJECT matches restWordSubject, with a public reply
// that does not and an internal note, written last, that does (with the internal
// word beside it). A leak would pick the note as the newest matching reply.
func addLaptop(t *testing.T, h *harness) ticket.Ticket {
	t.Helper()
	tk, err := h.ticketSvc.Create(t.Context(), ticket.CreateInput{
		DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		Subject: "The " + restWordSubject + " laptop", Description: "nothing relevant", CategoryID: h.catID,
		Priority: ticket.PriorityLow, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	for i, r := range []struct {
		body     string
		internal bool
	}{{"Looking into it now", false}, {"decoy " + restWordSubject + " " + mcpWordInternal, true}} {
		require.NoError(t, h.ticketStore.CreateReply(t.Context(), ticket.Reply{
			ID: uuid.New(), TicketID: tk.ID, AuthorID: &h.staffID, Body: r.body, BodyFormat: ticket.TextFormatPlain,
			Internal: r.internal, NotifyCustomer: !r.internal, CreatedAt: time.Now().UTC().Add(time.Hour + time.Duration(i)*time.Second),
		}))
	}
	return tk
}

func TestListTickets_ReplyMatchForStaff(t *testing.T) {
	testutil.EnsureReplySearchReady(t)
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newMCPReplyFixture(t, h)

	got, _ := restSearch(t, h, h.do, mcpWordPublic, nil)
	by := restIDs(got)
	require.Contains(t, by, f.printer.ID)
	m := by[f.printer.ID].ReplyMatch
	require.NotNil(t, m, "a ticket found through a reply carries it")
	require.False(t, m.Internal)
	require.Contains(t, m.Snippet, "\u0002"+mcpWordPublic+"\u0003", "the matched word is marked with control characters, not HTML")
	require.NotContains(t, m.Snippet, "<")

	got, _ = restSearch(t, h, h.do, mcpWordInternal, nil)
	by = restIDs(got)
	require.Contains(t, by, f.network.ID, "staff find a ticket through an internal note")
	require.NotNil(t, by[f.network.ID].ReplyMatch)
	require.True(t, by[f.network.ID].ReplyMatch.Internal)
}

// A ticket that matches on its own subject shows the newest matching reply it has
// among the ones the viewer may read.
func TestListTickets_ReplyMatchNeverShowsAnInternalNoteToARequester(t *testing.T) {
	testutil.EnsureReplySearchReady(t)
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newMCPReplyFixture(t, h)
	laptop := addLaptop(t, h)

	requesters := []struct {
		name string
		as   func(*testing.T, string, string, any) *http.Response
	}{{"requester key", h.doAsUser}}
	for _, who := range requesters {
		t.Run(who.name, func(t *testing.T) {
			// Only an internal note matches: the ticket is not found at all.
			got, _ := restSearch(t, h, who.as, mcpWordInternal, nil)
			by := restIDs(got)
			require.NotContains(t, by, f.network.ID, "its only match is an internal note")
			require.Contains(t, by, f.both.ID, "a PUBLIC reply holds the word")
			require.False(t, by[f.both.ID].ReplyMatch.Internal)

			// Found by its subject: no excerpt, because its only matching reply is a note.
			got, body := restSearch(t, h, who.as, restWordSubject, nil)
			by = restIDs(got)
			require.Contains(t, by, laptop.ID)
			require.Nil(t, by[laptop.ID].ReplyMatch, "no excerpt from an internal note, even on a ticket that matched by subject")
			require.NotContains(t, body, "decoy")

			// include_internal is not a parameter.
			got, _ = restSearch(t, h, who.as, mcpWordInternal, url.Values{"include_internal": {"true"}})
			require.Equal(t, map[uuid.UUID]bool{f.both.ID: true}, func() map[uuid.UUID]bool {
				m := map[uuid.UUID]bool{}
				for id := range restIDs(got) {
					m[id] = true
				}
				return m
			}())
		})
	}

	// Staff do get the note as the excerpt on that same ticket.
	got, _ := restSearch(t, h, h.do, restWordSubject, nil)
	by := restIDs(got)
	require.NotNil(t, by[laptop.ID].ReplyMatch)
	require.True(t, by[laptop.ID].ReplyMatch.Internal)
	require.Contains(t, by[laptop.ID].ReplyMatch.Snippet, "decoy")
}

// The invariant (design 2.5, rule 4), through REST: run the searches as a
// requester, delete every internal note, run them again. The bodies must be
// identical, excerpts included.
func TestListTickets_RequesterResultsDoNotDependOnInternalNotes(t *testing.T) {
	testutil.EnsureReplySearchReady(t)
	h, cleanup := newHarness(t)
	defer cleanup()
	newMCPReplyFixture(t, h)
	addLaptop(t, h)

	queries := []string{mcpWordPublic, mcpWordInternal, "zanz", mcpWordPublic + " " + mcpWordInternal, restWordSubject, "toner", "switch", "roller"}
	search := func() map[string]string {
		out := map[string]string{}
		for _, q := range queries {
			_, body := restSearch(t, h, h.doAsUser, q, nil)
			out[q] = body
		}
		return out
	}
	before := search()

	// Not vacuous: staff get results the requester does not.
	_, staffBody := restSearch(t, h, h.do, mcpWordInternal, nil)
	require.NotEqual(t, before[mcpWordInternal], staffBody)

	res, err := h.tx.ExecContext(t.Context(), `DELETE FROM ticket_replies WHERE internal`)
	require.NoError(t, err)
	n, _ := res.RowsAffected()
	require.Greater(t, n, int64(0))

	require.Equal(t, before, search(), "a requester's results changed when the internal notes were deleted")
}

// Not asked for: no excerpt, and no key at all.
func TestListTickets_NoReplyMatchUnlessAskedFor(t *testing.T) {
	testutil.EnsureReplySearchReady(t)
	h, cleanup := newHarness(t)
	defer cleanup()
	newMCPReplyFixture(t, h)

	for _, as := range []func(*testing.T, string, string, any) *http.Response{h.do, h.doAsUser} {
		res := as(t, http.MethodGet, "/api/v1/tickets?q=printer&scope=visible", nil)
		body, _ := io.ReadAll(res.Body)
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.NotContains(t, string(body), "reply_match")
	}
}

// While the reply index is being built the page is chosen without the reply arm,
// so it must show no excerpt as if replies had been searched.
func TestListTickets_NoReplyMatchWhileTheIndexIsBuilt(t *testing.T) {
	h, cleanup := newIsolatedHarness(t)
	defer cleanup()
	f := newMCPReplyFixture(t, h)

	res := h.do(t, http.MethodGet, "/api/v1/tickets?q=printer&search_replies=true&scope=visible", nil)
	body, _ := io.ReadAll(res.Body)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "building", res.Header.Get("X-Reply-Index"))
	require.Contains(t, string(body), f.printer.ID.String(), "the subject match is still listed")
	require.False(t, strings.Contains(string(body), "reply_match"))
}
