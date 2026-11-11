package server_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Until the reply search exists the store has nothing to report, so the headers
// are absent however the request is written. The app is same-origin and sets no
// CORS headers; a browser reads response headers freely, so nothing has to
// expose them. This pins both, so adding a cross-origin policy later has to
// decide about X-Replies-Searched and X-Reply-Index on purpose.
func TestListTickets_ReplySearchHeadersAreAbsentWithoutAVerdict(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	for _, q := range []string{"", "?q=alpaca", "?q=alpaca&search_replies=true"} {
		res := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets"+q, nil)
		require.Equal(t, http.StatusOK, res.StatusCode, q)
		require.Empty(t, res.Header.Get("X-Replies-Searched"), q)
		require.Empty(t, res.Header.Get("X-Reply-Index"), q)
		require.Empty(t, res.Header.Get("Access-Control-Expose-Headers"), q)
		require.Empty(t, res.Header.Get("Access-Control-Allow-Origin"), q)
	}
}
