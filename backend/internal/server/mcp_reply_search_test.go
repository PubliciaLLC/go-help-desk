package server_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// Reply search over MCP (advanced search, section 2.5, rule 4: the invariant
// test "runs at the store level, and again through REST and MCP").
//
// SECURITY-CRITICAL: a requester must never match on, or see a snippet of, an
// internal note. include_internal is computed from the caller's role and is not
// an argument; one passed anyway is ignored.
//
// The REST half of this lives with the REST handler rewrite (PR C), which is
// what makes GET /tickets take search_replies; the same fixture and assertions
// apply there.

const (
	mcpWordPublic   = "fusilade"
	mcpWordInternal = "zanzibar"
)

type mcpReplyFixture struct {
	printer, network, both ticket.Ticket
}

func newMCPReplyFixture(t *testing.T, h *harness) mcpReplyFixture {
	t.Helper()
	ctx := context.Background()
	mk := func(subject string) ticket.Ticket {
		tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
			DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
			Subject: subject, Description: "nothing relevant", CategoryID: h.catID,
			Priority: ticket.PriorityLow, ReporterUserID: &h.userID,
		})
		require.NoError(t, err)
		return tk
	}
	n := 0
	reply := func(tk ticket.Ticket, body string, internal bool) {
		n++
		require.NoError(t, h.ticketStore.CreateReply(ctx, ticket.Reply{
			ID: uuid.New(), TicketID: tk.ID, AuthorID: &h.staffID, Body: body, BodyFormat: ticket.TextFormatPlain,
			Internal: internal, NotifyCustomer: !internal, CreatedAt: time.Now().UTC().Add(time.Duration(n) * time.Second),
		}))
	}
	f := mcpReplyFixture{printer: mk("Printer jam"), network: mk("Network down"), both: mk("Both words")}
	reply(f.printer, "The "+mcpWordPublic+" roller squeaks", false)
	reply(f.printer, "Customer reuses "+mcpWordInternal+" toner", true)
	reply(f.network, "The "+mcpWordInternal+" switch is dead", true)
	reply(f.both, mcpWordPublic+" and "+mcpWordInternal+" together", false)
	return f
}

type mcpListed struct {
	ID         uuid.UUID `json:"id"`
	ReplyMatch *struct {
		ReplyID  uuid.UUID `json:"reply_id"`
		Internal bool      `json:"internal"`
		Snippet  string    `json:"snippet"`
	} `json:"reply_match"`
}

// searchMCP is list_tickets with q and search_replies, decoded.
func searchMCP(t *testing.T, c mcpCaller, q string, extra map[string]any) []mcpListed {
	t.Helper()
	args := map[string]any{"q": q, "search_replies": true}
	for k, v := range extra {
		args[k] = v
	}
	var out []mcpListed
	text := toolText(t, c.call(t, "list_tickets", args))
	require.NoError(t, json.Unmarshal([]byte(text), &out), "list_tickets said: %s", text)
	return out
}

func idsOf(l []mcpListed) map[uuid.UUID]bool {
	m := map[uuid.UUID]bool{}
	for _, e := range l {
		m[e.ID] = true
	}
	return m
}

func TestMCP_ReplySearch_RequesterNeverMatchesInternalNotes(t *testing.T) {
	testutil.EnsureReplySearchReady(t)
	h, cleanup := newHarness(t)
	defer cleanup()
	f := newMCPReplyFixture(t, h)

	for _, tr := range mcpTransports {
		t.Run(tr.name, func(t *testing.T) {
			requester := tr.open(t, h, h.userKey)
			staff := tr.open(t, h, h.apiKey)

			t.Run("only an internal note matches", func(t *testing.T) {
				got := searchMCP(t, requester, mcpWordInternal, nil)
				require.Equal(t, map[uuid.UUID]bool{f.both.ID: true}, idsOf(got),
					"a requester finds only the ticket whose PUBLIC reply holds the word")
				require.NotNil(t, got[0].ReplyMatch)
				require.False(t, got[0].ReplyMatch.Internal)

				seen := idsOf(searchMCP(t, staff, mcpWordInternal, nil))
				require.True(t, seen[f.network.ID], "staff do find a ticket through an internal note")
				require.True(t, seen[f.printer.ID])
			})

			t.Run("include_internal is not an argument", func(t *testing.T) {
				got := searchMCP(t, requester, mcpWordInternal, map[string]any{"include_internal": true})
				require.Equal(t, map[uuid.UUID]bool{f.both.ID: true}, idsOf(got))
			})

			t.Run("off unless asked for", func(t *testing.T) {
				text := toolText(t, requester.call(t, "list_tickets", map[string]any{"q": mcpWordPublic}))
				var out []mcpListed
				require.NoError(t, json.Unmarshal([]byte(text), &out))
				require.False(t, idsOf(out)[f.printer.ID], "replies are not searched without search_replies")
			})
		})
	}
}

// The invariant (design 2.5, rule 4): run the searches as a requester, delete
// every internal note, run them again. The results must be identical.
func TestMCP_ReplySearch_RequesterResultsDoNotDependOnInternalNotes(t *testing.T) {
	testutil.EnsureReplySearchReady(t)
	h, cleanup := newHarness(t)
	defer cleanup()
	newMCPReplyFixture(t, h)

	queries := []string{mcpWordPublic, mcpWordInternal, "zanz", mcpWordPublic + " " + mcpWordInternal, "toner", "switch", "roller"}
	type run map[string]map[string][]mcpListed // transport -> query -> results
	search := func() run {
		out := run{}
		for _, tr := range mcpTransports {
			c := tr.open(t, h, h.userKey)
			out[tr.name] = map[string][]mcpListed{}
			for _, q := range queries {
				out[tr.name][q] = searchMCP(t, c, q, nil)
			}
		}
		return out
	}

	before := search()

	// Not vacuous: staff get results the requester does not.
	for _, tr := range mcpTransports {
		staff := tr.open(t, h, h.apiKey)
		require.Greater(t, len(searchMCP(t, staff, mcpWordInternal, nil)), len(before[tr.name][mcpWordInternal]))
	}

	// h.tx is the harness transaction: the delete rolls back with the test.
	res, err := h.tx.ExecContext(context.Background(), `DELETE FROM ticket_replies WHERE internal`)
	require.NoError(t, err)
	n, _ := res.RowsAffected()
	require.Greater(t, n, int64(0))

	require.Equal(t, before, search(), "a requester's results changed when the internal notes were deleted")
}

// toolTexts is every content block of a tool result, in order.
func toolTexts(t *testing.T, payload string) []string {
	t.Helper()
	var out struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(payload), &out), "payload: %s", payload)
	var texts []string
	for _, c := range out.Result.Content {
		texts = append(texts, c.Text)
	}
	return texts
}

// While the server is still filling and indexing the replies after an upgrade,
// a search that asks for them is answered from subject, description and number
// only, and the caller is told so. Without the second block an agent would
// take the shorter list for the whole answer.
func TestMCP_ReplySearch_SaysWhenRepliesWereNotSearched(t *testing.T) {
	h, cleanup := newIsolatedHarness(t) // no reply indexes: not prepared yet
	defer cleanup()
	f := newMCPReplyFixture(t, h)

	for _, tr := range mcpTransports {
		t.Run(tr.name, func(t *testing.T) {
			staff := tr.open(t, h, h.apiKey)

			// A word only a reply holds: nothing to find yet, and a note saying why.
			texts := toolTexts(t, staff.call(t, "list_tickets", map[string]any{"q": mcpWordInternal, "search_replies": true}))
			require.Len(t, texts, 2, "the list and a note: %v", texts)
			require.JSONEq(t, `[]`, texts[0])
			require.Contains(t, texts[1], "not searched")
			require.Contains(t, texts[1], "still being prepared")

			// A subject match is still returned, with no excerpt.
			texts = toolTexts(t, staff.call(t, "list_tickets", map[string]any{"q": "printer", "search_replies": true}))
			require.Len(t, texts, 2)
			var got []mcpListed
			require.NoError(t, json.Unmarshal([]byte(texts[0]), &got))
			require.Equal(t, map[uuid.UUID]bool{f.printer.ID: true}, idsOf(got))
			require.Nil(t, got[0].ReplyMatch, "no excerpt for a page chosen without replies")

			// Not asked for: the answer is the list alone.
			texts = toolTexts(t, staff.call(t, "list_tickets", map[string]any{"q": "printer"}))
			require.Len(t, texts, 1)
		})
	}
}

// A page chosen without the reply arm (the index still being built, or the time
// limit passed) carries no excerpt over MCP either, even for a ticket that is
// listed by its subject and holds a matching reply.
func TestMCP_ReplySearch_NoExcerptWithoutTheReplyArm(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T) (*harness, func())
	}{
		{"index still being built", func(t *testing.T) (*harness, func()) { return newIsolatedHarness(t) }},
		{"time limit passed", func(t *testing.T) (*harness, func()) {
			testutil.EnsureReplySearchReady(t)
			harnessReplyTimeout = time.Nanosecond
			h, cleanup := newHarness(t)
			return h, func() { cleanup(); harnessReplyTimeout = ticketstore.DefaultReplyTimeout }
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := tc.setup(t)
			defer cleanup()
			f := newMCPReplyFixture(t, h)
			addPrinterNote(t, h, f)
			for _, tr := range mcpTransports {
				t.Run(tr.name, func(t *testing.T) {
					staff := tr.open(t, h, h.apiKey)
					texts := toolTexts(t, staff.call(t, "list_tickets", map[string]any{"q": "printer", "search_replies": true}))
					require.Len(t, texts, 2, "the list and the note")
					var got []mcpListed
					require.NoError(t, json.Unmarshal([]byte(texts[0]), &got))
					require.True(t, idsOf(got)[f.printer.ID], "listed by its subject")
					for _, l := range got {
						require.Nil(t, l.ReplyMatch, "no excerpt for a page chosen without the reply arm")
					}
				})
			}
		})
	}
}
