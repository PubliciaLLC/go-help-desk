package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// #349: a guest write that races the close — past the middleware, refused by
// the service — is answered with the SAME bytes as the middleware's refusal of
// a bad link, not the 409 "this ticket is closed" a signed-in reporter gets. A
// guest is never told that a ticket exists in a state that refuses them.
func TestGuestWriteError_ClosedIsTheGenericNotFound(t *testing.T) {
	// What the middleware sends for a link that does not resolve.
	mw := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/guest/replies", nil)
	req.Header.Set("Authorization", "Guest bad")
	h := authmw.GuestAuth(func(context.Context, string) (string, error) { return "", errors.New("no") })
	h(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(mw, req)
	require.Equal(t, http.StatusNotFound, mw.Code, "precondition")

	for _, err := range []error{
		ticket.ErrClosed,
		fmt.Errorf("cannot reply to ticket: %w", ticket.ErrClosed),
	} {
		rec := httptest.NewRecorder()
		guestWriteError(rec, err)
		require.Equal(t, mw.Code, rec.Code)
		require.Equal(t, mw.Header().Get("Content-Type"), rec.Header().Get("Content-Type"))
		require.Equal(t, mw.Body.String(), rec.Body.String(),
			"a closed ticket must be refused byte-for-byte as a bad link is")
	}

	// Everything else still answers as it did: a Resolved ticket past its
	// window is the guest's own ticket and says so.
	rec := httptest.NewRecorder()
	guestWriteError(rec, ticket.ErrReopenWindowClosed)
	require.Equal(t, http.StatusConflict, rec.Code)
}
