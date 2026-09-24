package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// A reply says who wrote it.
//
// The thread had nothing but author_id to render and rendered that, so every
// message from a registered account showed as a bare UUID: a staff member
// reading a ticket could not tell who had said what. The page cannot look the
// name up either — a reporting user is not allowed to list users, and should
// not be — so the name comes back with the reply.
func TestListReplies_CarryTheAuthorsName(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Who said this", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	res := h.do(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/replies",
		map[string]any{"body": "Looking into it."})
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	list := h.do(t, http.MethodGet, "/api/v1/tickets/"+tk.ID.String()+"/replies", nil)
	defer list.Body.Close()
	require.Equal(t, http.StatusOK, list.StatusCode)

	var replies []ticket.Reply
	require.NoError(t, json.NewDecoder(list.Body).Decode(&replies))
	require.Len(t, replies, 1)
	require.NotNil(t, replies[0].AuthorID)
	require.NotEmpty(t, replies[0].AuthorName,
		"the reply carries an id and no name, so the page can only print the id")
	require.NotEqual(t, replies[0].AuthorID.String(), replies[0].AuthorName)
}

// A guest's reply has no account behind it, so the name is empty rather than
// invented. That is the one case where author_id is genuinely NULL.
func TestListReplies_AGuestReplyHasNoAuthorName(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	email := "visitor@example.com"
	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "From a visitor", Description: "x", CategoryID: h.catID,
		GuestEmail: &email, GuestName: "A Visitor",
	})
	require.NoError(t, err)

	require.NoError(t, h.ticketStore.CreateReply(ctx, ticket.Reply{
		ID: uuid.New(), TicketID: tk.ID, Body: "Any news?", CreatedAt: time.Now().UTC(),
	}))

	replies, err := h.ticketSvc.ListReplies(ctx, tk.ID)
	require.NoError(t, err)
	require.Len(t, replies, 1)
	require.Nil(t, replies[0].AuthorID)
	require.Empty(t, replies[0].AuthorName)
}
