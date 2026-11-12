package server_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// A caller's mistake is the caller's to be told about, and is not an error in
// the server's log. Three families reached storeErr without being recognised
// and came back as "<op> failed" with a line at ERROR:
//   - a malformed list_tickets argument (buildListFilter returned a bare error);
//   - a move into a retired status, or into one that does not exist;
//   - a move the caller's role may not make (staff closing a ticket).
//
// REST answers each of them with a 4xx and the reason, so an agent driving MCP
// was left retrying a call that could never work, and an operator watching the
// log saw faults that were not faults.

// syncBuffer is a log sink the MCP handler goroutines and the test can share.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestMCP_CallerMistakesAreAnsweredAndNotLoggedAsErrors(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk := reporterOpenTicket(t, h)
	id := tk.ID.String()

	retired := mkStatus(t, h, "Retired for MCP")
	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/statuses/"+retired, map[string]any{"active": false})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	closed := statusIDNamed(t, h, ticket.StatusNameClosed).String()

	cases := []struct {
		name, tool string
		args       map[string]any
		want       string
	}{
		{"a priority that is not one", "list_tickets", map[string]any{"priority": "urgent"}, "priority must be one of"},
		{"a status id that is not an id", "list_tickets", map[string]any{"status_id": "nope"}, "invalid status_id"},
		{"a category id that is not an id", "list_tickets", map[string]any{"category_id": "nope"}, "invalid category_id"},
		{"a retired status", "update_ticket_status", map[string]any{"ticket_id": id, "status_id": retired}, "inactive"},
		{"a status that does not exist", "update_ticket_status", map[string]any{"ticket_id": id, "status_id": uuid.NewString()}, "not found"},
		{"staff closing a ticket", "update_ticket_status", map[string]any{"ticket_id": id, "status_id": closed}, "permission"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logs := &syncBuffer{}
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
			t.Cleanup(func() { slog.SetDefault(prev) })

			c := openMCP(t, h, h.apiKey)
			defer c.closeBody()
			got := c.call(t, tc.tool, tc.args)

			require.Contains(t, got, `"isError":true`, got)
			require.Contains(t, got, tc.want, "the caller must be told what was wrong: %s", got)
			require.NotRegexp(t, `(list tickets|update ticket status) failed`, got, "a caller's mistake is not a server failure")
			require.NotContains(t, logs.String(), "level=ERROR", "a caller's mistake is not an error to log")
		})
	}
}
