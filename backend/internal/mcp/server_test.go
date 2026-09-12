package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// These tests pin the invariant that every MCP tool refuses to act without an
// authenticated identity in its context.
//
// Before this, /mcp/ was mounted on the root ServeMux outside the middleware
// chain that guards /api/, so every tool was reachable with no credentials at
// all: an unauthenticated caller could read any ticket by tracking number and
// post replies naming any user as the author. Verified against a local
// instance — get_ticket returned a ticket body and add_reply persisted.
//
// The transport-level fix is Server.ProtectMCP in internal/server, covered by
// TestProtectMCP there. The checks below are the second line: if the wrapper is
// ever removed or a new mount forgets it, the tools still refuse rather than
// silently serving whoever asked.
//
// s.tickets is deliberately nil. Each handler must reject before reaching the
// service, so a nil service is the strongest possible assertion that no work
// happens — if one ever stops checking, the test panics instead of passing.

func callToolRequest(name string, args map[string]any) mcpgo.CallToolRequest {
	var req mcpgo.CallToolRequest
	req.Params.Name = name
	req.Params.Arguments = args
	return req
}

func TestTools_RefuseWithoutAuthenticatedActor(t *testing.T) {
	s := &Server{} // nil ticket service on purpose — see file comment

	ticketID := uuid.New().String()

	cases := []struct {
		name    string
		call    func(context.Context) (*mcpgo.CallToolResult, error)
		request map[string]any
	}{
		{
			name: "get_ticket",
			call: func(ctx context.Context) (*mcpgo.CallToolResult, error) {
				return s.handleGetTicket(ctx, callToolRequest("get_ticket", map[string]any{"id": ticketID}))
			},
		},
		{
			name: "create_ticket",
			call: func(ctx context.Context) (*mcpgo.CallToolResult, error) {
				return s.handleCreateTicket(ctx, callToolRequest("create_ticket", map[string]any{
					"subject":     "Opened by a stranger",
					"category_id": uuid.New().String(),
				}))
			},
		},
		{
			name: "add_reply",
			call: func(ctx context.Context) (*mcpgo.CallToolResult, error) {
				return s.handleAddReply(ctx, callToolRequest("add_reply", map[string]any{
					"ticket_id": ticketID,
					"body":      "Posted by a stranger",
					// A declared author must not be honoured either.
					"author_user_id": uuid.New().String(),
				}))
			},
		},
		{
			name: "list_tickets",
			call: func(ctx context.Context) (*mcpgo.CallToolResult, error) {
				return s.handleListTickets(ctx, callToolRequest("list_tickets", map[string]any{
					"assignee_user_id": uuid.New().String(),
				}))
			},
		},
		{
			name: "assign_ticket",
			call: func(ctx context.Context) (*mcpgo.CallToolResult, error) {
				return s.handleAssignTicket(ctx, callToolRequest("assign_ticket", map[string]any{
					"ticket_id":        ticketID,
					"assignee_user_id": uuid.New().String(),
					"actor_user_id":    uuid.New().String(),
				}))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := tc.call(context.Background()) // no actor in context

			require.NoError(t, err, "the tool reports refusal in its result, not as a transport error")
			require.NotNil(t, res)
			require.True(t, res.IsError, "%s must refuse an unauthenticated call", tc.name)
			require.Contains(t, resultText(t, res), "unauthenticated",
				"the refusal must say why")
		})
	}
}

// TestActorFrom_RoundTrips covers the context plumbing the tools depend on. If
// the key type or the lookup ever drift apart, every tool silently falls back
// to refusing — which is safe, but would break MCP entirely and is worth
// catching here rather than in production.
func TestActorFrom_RoundTrips(t *testing.T) {
	require.Nil(t, actorFrom(context.Background()), "a bare context carries no actor")

	want := &authmw.Actor{UserID: uuid.New(), Role: user.RoleStaff, MFAPassed: true}
	got := actorFrom(context.WithValue(context.Background(), actorCtxKey{}, want))

	require.NotNil(t, got)
	require.Equal(t, want.UserID, got.UserID)
	require.Equal(t, user.RoleStaff, got.Role)
}

// TestRegisterTools_DeclareNoCallerIdentity is the regression guard on the
// impersonation half of the defect. add_reply and assign_ticket used to accept
// author_user_id and actor_user_id, so the caller named itself and the server
// believed it. Those parameters are gone; identity comes from the request.
//
// reporter_user_id survives deliberately: it names the SUBJECT of a ticket, not
// the caller, because staff legitimately open tickets on someone's behalf.
func TestRegisterTools_DeclareNoCallerIdentity(t *testing.T) {
	s := New(nil, nil)

	tools := s.mcp.ListTools()
	require.NotEmpty(t, tools, "tools must be registered")

	for _, st := range tools {
		schema, err := st.Tool.InputSchema.MarshalJSON()
		require.NoError(t, err)
		text := string(schema)

		require.NotContains(t, text, "author_user_id",
			"tool %q must not accept a caller-declared author", st.Tool.Name)
		require.NotContains(t, text, "actor_user_id",
			"tool %q must not accept a caller-declared actor", st.Tool.Name)
	}
}

// resultText flattens a tool result's content for assertions.
func resultText(t *testing.T, res *mcpgo.CallToolResult) string {
	t.Helper()
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcpgo.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
