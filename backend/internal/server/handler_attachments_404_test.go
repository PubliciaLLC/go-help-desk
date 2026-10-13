package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// Attachment not found returns 404, not 500 (#418).
func TestDownloadAttachment_UnknownAttachmentReturns404(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	// Create a ticket so the ticket ID is valid.
	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Test", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	// Use a non-existent attachment ID.
	attID := uuid.New()
	res := h.do(t, http.MethodGet, "/api/v1/tickets/"+tk.ID.String()+"/attachments/"+attID.String(), nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode)
}

// Recheck reputation on unknown attachment returns 404, not 500 (#418).
func TestRecheckAttachmentReputation_UnknownAttachmentReturns404(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	// Create a ticket so the ticket ID is valid.
	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Test", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	// Use a non-existent attachment ID.
	attID := uuid.New()
	res := h.do(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/attachments/"+attID.String()+"/reputation/recheck", nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode)
}
