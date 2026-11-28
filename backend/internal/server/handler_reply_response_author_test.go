package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// The reply the POST answers with names its author too, so a client can show
// it without listing the thread again (#420). The list already does
// (reply_author_test.go).
func TestAddReply_ResponseCarriesTheAuthorsName(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		Subject: "Who said this", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	res := h.do(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/replies",
		map[string]any{"body": "Looking into it."})
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	var reply ticket.Reply
	require.NoError(t, json.NewDecoder(res.Body).Decode(&reply))
	require.NotNil(t, reply.AuthorID)
	require.NotEmpty(t, reply.AuthorName)
	require.NotEqual(t, reply.AuthorID.String(), reply.AuthorName)
}
