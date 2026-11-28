package server_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// DELETE /tickets answers 405, and the methods the collection does offer are
// all in the Allow header. chi writes one Allow line per method, which
// RFC 9110 treats as the same as "GET, POST"; a client that reads only the
// first line (Go's Header.Get, many scripts) sees just "GET". That looked like
// an Allow header that omitted POST (#421). chi has no hook to join the lines
// short of wrapping every response writer, so this pins what is on the wire and
// openapi.yaml tells clients to read every line.
func TestTicketsCollection_AllowHeaderListsEveryMethod(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	for _, path := range []string{"/api/v1/tickets", "/api/v1/tickets/"} {
		t.Run(path, func(t *testing.T) {
			resp := h.doAsAdmin(t, http.MethodDelete, path, nil)
			resp.Body.Close()
			require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
			require.ElementsMatch(t, []string{"GET", "POST"}, resp.Header.Values("Allow"))
		})
	}
}
