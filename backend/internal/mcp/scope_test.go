package mcp

import (
	"context"
	"testing"

	"github.com/google/uuid"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// ProtectMCP authenticates and checks the role, and until this change nothing
// on the MCP surface read scopes at all. A credential carrying none — or only
// users:read — could read every ticket and create, reply, assign and re-status
// through MCP while the same credential got 403 on the REST equivalents.
//
// s.tickets is nil on purpose, as elsewhere in this package: a missed check
// becomes a panic rather than a silently passing test.

func ctxAsMachine(role user.Role, scopes []string) context.Context {
	a := &authmw.Actor{
		UserID: uuid.New(), Role: role, MFAPassed: true,
		Machine: true, Scopes: scopes,
	}
	return context.WithValue(context.Background(), actorCtxKey{}, a)
}

func readTools(s *Server) map[string]func(context.Context) (*mcpgo.CallToolResult, error) {
	return map[string]func(context.Context) (*mcpgo.CallToolResult, error){
		"get_ticket": func(ctx context.Context) (*mcpgo.CallToolResult, error) {
			return scoped(mcpRead, s.handleGetTicket)(ctx,
				callToolRequest("get_ticket", map[string]any{"id": uuid.New().String()}))
		},
		"list_tickets": func(ctx context.Context) (*mcpgo.CallToolResult, error) {
			return scoped(mcpRead, s.handleListTickets)(ctx,
				callToolRequest("list_tickets", map[string]any{}))
		},
		"list_categories": func(ctx context.Context) (*mcpgo.CallToolResult, error) {
			return scoped(mcpRead, s.handleListCategories)(ctx,
				callToolRequest("list_categories", map[string]any{}))
		},
	}
}

func writeTools(s *Server) map[string]func(context.Context) (*mcpgo.CallToolResult, error) {
	return map[string]func(context.Context) (*mcpgo.CallToolResult, error){
		"create_ticket": func(ctx context.Context) (*mcpgo.CallToolResult, error) {
			return scoped(mcpWrite, s.handleCreateTicket)(ctx,
				callToolRequest("create_ticket", map[string]any{
					"subject": "x", "category_id": uuid.New().String()}))
		},
		"add_reply": func(ctx context.Context) (*mcpgo.CallToolResult, error) {
			return scoped(mcpWrite, s.handleAddReply)(ctx,
				callToolRequest("add_reply", map[string]any{
					"ticket_id": uuid.New().String(), "body": "x"}))
		},
		"assign_ticket": func(ctx context.Context) (*mcpgo.CallToolResult, error) {
			return scoped(mcpWrite, s.handleAssignTicket)(ctx,
				callToolRequest("assign_ticket", map[string]any{
					"ticket_id": uuid.New().String(), "assignee_user_id": uuid.New().String()}))
		},
		"update_ticket_status": func(ctx context.Context) (*mcpgo.CallToolResult, error) {
			return scoped(mcpWrite, s.handleUpdateTicketStatus)(ctx,
				callToolRequest("update_ticket_status", map[string]any{
					"ticket_id": uuid.New().String(), "status": "Resolved"}))
		},
	}
}

func requireRefusedForScope(t *testing.T, res *mcpgo.CallToolResult, err error) {
	t.Helper()
	require.NoError(t, err)
	require.NotNil(t, res)
	require.True(t, res.IsError, "the tool must refuse")
	require.Contains(t, resultText(t, res), "scope",
		"the refusal must say the credential lacks a scope")
}

// A credential with no scopes reaches nothing on MCP, exactly as on REST.
func TestMCP_NoScopesReachesNoTool(t *testing.T) {
	s := &Server{}
	ctx := ctxAsMachine(user.RoleAdmin, nil)

	for name, call := range readTools(s) {
		t.Run("read "+name, func(t *testing.T) {
			res, err := call(ctx)
			requireRefusedForScope(t, res, err)
		})
	}
	for name, call := range writeTools(s) {
		t.Run("write "+name, func(t *testing.T) {
			res, err := call(ctx)
			requireRefusedForScope(t, res, err)
		})
	}
}

// A scope for a different resource grants nothing here, even at admin role.
func TestMCP_UnrelatedScopeReachesNoTool(t *testing.T) {
	s := &Server{}
	ctx := ctxAsMachine(user.RoleAdmin, []string{"users:read", "users:write", "settings:write"})

	for name, call := range readTools(s) {
		t.Run(name, func(t *testing.T) {
			res, err := call(ctx)
			requireRefusedForScope(t, res, err)
		})
	}
}

// tickets:read must not be enough to write. This is the asymmetry that makes a
// read-only integration actually read-only.
func TestMCP_ReadScopeCannotWrite(t *testing.T) {
	s := &Server{}
	ctx := ctxAsMachine(user.RoleAdmin, []string{"tickets:read"})

	for name, call := range writeTools(s) {
		t.Run(name, func(t *testing.T) {
			res, err := call(ctx)
			requireRefusedForScope(t, res, err)
		})
	}
}

// And the scope check must not be the only thing standing there: a session
// carries no scopes and must still work, or the browser breaks.
func TestMCP_SessionIsNotScoped(t *testing.T) {
	s := &Server{}
	a := &authmw.Actor{UserID: uuid.New(), Role: user.RoleAdmin, MFAPassed: true}
	ctx := context.WithValue(context.Background(), actorCtxKey{}, a)

	// Reaches the handler rather than the scope check. With s.tickets nil the
	// handler panics, which is the proof it got past `scoped`.
	require.Panics(t, func() {
		_, _ = scoped(mcpRead, s.handleListTickets)(ctx,
			callToolRequest("list_tickets", map[string]any{}))
	}, "a session must not be refused for lack of scopes")
}

// Note on what is NOT tested here: whether each tool is actually REGISTERED
// with its wrapper. These tests compose `scoped` themselves, so they cannot see
// a registration that forgot it — an earlier version of this file claimed to
// check exactly that and did not. The registration is pinned end-to-end in
// internal/server/protect_mcp_scope_test.go, which drives the real registered
// tools over the real SSE transport with a restricted key.
