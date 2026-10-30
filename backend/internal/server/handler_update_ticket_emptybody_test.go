package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// PATCH /tickets/{id} with no body at all means "no change", the same as {}.
// It used to answer 400 "invalid JSON", which made a client that had nothing
// to change handle an error for a request that asked for nothing (#420).
// Malformed JSON is still a 400.
func TestUpdateTicket_EmptyBodyIsNoChange(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		Subject: "Nothing to change", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)
	path := "/api/v1/tickets/" + tk.ID.String()

	t.Run("no body", func(t *testing.T) {
		res := h.do(t, http.MethodPatch, path, nil)
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
	})
	t.Run("empty object", func(t *testing.T) {
		res := h.do(t, http.MethodPatch, path, map[string]any{})
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
	})
	t.Run("malformed JSON is still refused", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPatch, path, strings.NewReader("{"))
		req.Header.Set("Authorization", "ApiKey "+h.apiKey)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		h.srv.ServeHTTP(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})
}
