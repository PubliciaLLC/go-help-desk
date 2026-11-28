package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

// ticketIDFrom is only meaningful behind requireTicketAccess, which leaves the
// resolved ticket in the request context. A route under /tickets/{id} that was
// registered outside it would panic on its first request, so this fails the
// build of the PR instead (#420).
func TestTicketRoutes_AreAllBehindTheAccessGate(t *testing.T) {
	s := routeOnlyServer(nil)

	seen := 0
	err := chi.Walk(s.router, func(method, route string, h http.Handler, mws ...func(http.Handler) http.Handler) error {
		if !strings.Contains(route, "/tickets/{id}") {
			return nil
		}
		seen++
		gated := false
		for _, mw := range mws {
			if strings.Contains(funcName(mw), "requireTicketAccess") {
				gated = true
			}
		}
		require.True(t, gated, "%s %s is under /tickets/{id} but not behind requireTicketAccess", method, route)
		return nil
	})
	require.NoError(t, err)
	require.Greater(t, seen, 20, "the walk found almost no /tickets/{id} routes, so it is not looking at the router")
}
