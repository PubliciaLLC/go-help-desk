package server_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// A request that did not ask for reply search gets no header, whatever the
// store did. The app is same-origin and sets no CORS headers; a browser reads
// response headers freely, so nothing has to expose them. This pins both, so
// adding a cross-origin policy later has to decide about X-Replies-Searched and
// X-Reply-Index on purpose.
func TestListTickets_ReplySearchHeadersAreAbsentUnlessAskedFor(t *testing.T) {
	testutil.EnsureReplySearchReady(t)
	h, cleanup := newHarness(t)
	defer cleanup()

	for _, q := range []string{"", "?q=alpaca", "?q=alpaca&search_replies=false"} {
		res := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets"+q, nil)
		require.Equal(t, http.StatusOK, res.StatusCode, q)
		require.Empty(t, res.Header.Get("X-Replies-Searched"), q)
		require.Empty(t, res.Header.Get("X-Reply-Index"), q)
		require.Empty(t, res.Header.Get("Access-Control-Expose-Headers"), q)
		require.Empty(t, res.Header.Get("Access-Control-Allow-Origin"), q)
	}
	// Asked for, but with nothing to search for: also nothing to say.
	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets?search_replies=true", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Empty(t, res.Header.Get("X-Replies-Searched"))
}

// The three things the store can say about a reply search, through the real
// store and the real handler: searched, the index still being built, and the
// time limit passed. Each tells the page what to do (nothing, "Reply search is
// still being prepared", "replies were not fully searched").
func TestListTickets_ReplySearchHeaders(t *testing.T) {
	const get = "/api/v1/tickets?q=alpaca&search_replies=true"

	t.Run("searched", func(t *testing.T) {
		testutil.EnsureReplySearchReady(t)
		h, cleanup := newHarness(t)
		defer cleanup()
		res := h.doAsAdmin(t, http.MethodGet, get, nil)
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Equal(t, "true", res.Header.Get("X-Replies-Searched"))
		require.Empty(t, res.Header.Get("X-Reply-Index"))
	})

	t.Run("the index is still being built", func(t *testing.T) {
		h, cleanup := newIsolatedHarness(t) // a database of its own, with no reply indexes
		defer cleanup()
		res := h.doAsAdmin(t, http.MethodGet, get, nil)
		require.Equal(t, http.StatusOK, res.StatusCode, "the page still gets its tickets")
		require.Equal(t, "false", res.Header.Get("X-Replies-Searched"))
		require.Equal(t, "building", res.Header.Get("X-Reply-Index"))
	})

	t.Run("the time limit passed", func(t *testing.T) {
		testutil.EnsureReplySearchReady(t)
		harnessReplyTimeout = time.Nanosecond
		defer func() { harnessReplyTimeout = ticketstore.DefaultReplyTimeout }()
		h, cleanup := newHarness(t)
		defer cleanup()
		res := h.doAsAdmin(t, http.MethodGet, get, nil)
		require.Equal(t, http.StatusOK, res.StatusCode, "the tickets that match on their own text are still returned")
		require.Equal(t, "false", res.Header.Get("X-Replies-Searched"))
		require.Empty(t, res.Header.Get("X-Reply-Index"), "the index is ready; the search was too slow")
	})
}
