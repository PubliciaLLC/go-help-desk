// Package mcp exposes help desk operations as an MCP (Model Context Protocol)
// server for AI tool integration, on top of the existing service layer.
//
// This package does NOT authenticate anything itself. The handler it returns
// must be wrapped by Server.ProtectMCP in internal/server, which applies the
// same middleware chain as /api/ and restricts the surface to staff and
// administrators. Mounting Handler() bare leaves every tool reachable with no
// credentials.
//
// Every tool takes the acting identity from the authenticated request, never
// from its arguments. An earlier version accepted actor_user_id and
// author_user_id as tool parameters and hardcoded RoleStaff, so a caller
// declared who they were and the server believed it.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
	"github.com/publiciallc/go-help-desk/backend/internal/version"
)

// actorCtxKey carries the authenticated actor into tool handlers.
type actorCtxKey struct{}

// ErrNoActor is returned to a tool caller whose request carried no
// authenticated identity. Reaching it means ProtectMCP was not applied, since
// the chain rejects such requests before they get here — so it is a
// defence-in-depth check, not an expected path.
const noActorMessage = "unauthenticated: this server must be mounted behind ProtectMCP"

// actorFrom returns the authenticated actor for a tool call.
func actorFrom(ctx context.Context) *authmw.Actor {
	a, _ := ctx.Value(actorCtxKey{}).(*authmw.Actor)
	return a
}

// Server wraps the MCP server and wires up help desk tools.
type Server struct {
	mcp     *mcpserver.MCPServer
	tickets *ticket.Service

	// prefix returns the instance's tracking-number prefix. A function rather
	// than the admin service itself: this package needs one string, and taking
	// the whole service to get it would couple an integration surface to
	// settings management.
	prefix func(context.Context) string
}

// New creates a Server and registers all MCP tools.
func New(tickets *ticket.Service, prefix func(context.Context) string) *Server {
	if prefix == nil {
		// Tests and any caller that does not care still mint well-formed
		// numbers rather than an empty prefix.
		prefix = func(context.Context) string { return ticket.DefaultTrackingPrefix }
	}
	s := &Server{
		mcp:     mcpserver.NewMCPServer("go-help-desk", version.Version),
		tickets: tickets,
		prefix:  prefix,
	}
	s.registerTools()
	return s
}

// Handler returns the SSE HTTP handler for MCP clients.
//
// Must be wrapped by Server.ProtectMCP; see the package comment.
//
// The context func runs in the message handler, not only at SSE connect, so
// each tool call sees the actor of the request that made it rather than the
// actor that happened to open the stream.
func (s *Server) Handler() *mcpserver.SSEServer {
	return mcpserver.NewSSEServer(s.mcp,
		mcpserver.WithStaticBasePath("/mcp"),
		mcpserver.WithSSEContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			if a := authmw.GetActor(r); a != nil {
				return context.WithValue(ctx, actorCtxKey{}, a)
			}
			return ctx
		}),
	)
}

func (s *Server) registerTools() {
	s.mcp.AddTool(mcpgo.NewTool(
		"get_ticket",
		mcpgo.WithDescription("Get a ticket by its UUID or tracking number (e.g. OHD-2026-000001)"),
		mcpgo.WithString("id", mcpgo.Required(), mcpgo.Description("Ticket UUID or tracking number")),
	), s.handleGetTicket)

	s.mcp.AddTool(mcpgo.NewTool(
		"create_ticket",
		mcpgo.WithDescription("Open a new help desk ticket"),
		mcpgo.WithString("subject", mcpgo.Required(), mcpgo.Description("Short summary")),
		mcpgo.WithString("description", mcpgo.Description("Full description")),
		mcpgo.WithString("category_id", mcpgo.Required(), mcpgo.Description("Category UUID")),
		mcpgo.WithString("priority", mcpgo.Description("critical|high|medium|low (default: medium)")),
		// reporter_user_id is the SUBJECT of the ticket, not the caller —
		// staff legitimately open tickets on someone's behalf. It defaults to
		// the caller when omitted.
		mcpgo.WithString("reporter_user_id", mcpgo.Description("Reporter user UUID (defaults to the authenticated caller)")),
	), s.handleCreateTicket)

	s.mcp.AddTool(mcpgo.NewTool(
		"add_reply",
		mcpgo.WithDescription("Add a reply to a ticket"),
		mcpgo.WithString("ticket_id", mcpgo.Required(), mcpgo.Description("Ticket UUID")),
		mcpgo.WithString("body", mcpgo.Required(), mcpgo.Description("Reply body")),
		// No author parameter: the author is the authenticated caller.
	), s.handleAddReply)

	s.mcp.AddTool(mcpgo.NewTool(
		"list_tickets",
		mcpgo.WithDescription("List tickets assigned to a user"),
		mcpgo.WithString("assignee_user_id", mcpgo.Required(), mcpgo.Description("Assignee user UUID")),
		mcpgo.WithNumber("limit", mcpgo.Description("Max results (default 20)")),
		mcpgo.WithNumber("offset", mcpgo.Description("Pagination offset")),
	), s.handleListTickets)

	s.mcp.AddTool(mcpgo.NewTool(
		"assign_ticket",
		mcpgo.WithDescription("Assign a ticket to a user or group"),
		mcpgo.WithString("ticket_id", mcpgo.Required(), mcpgo.Description("Ticket UUID")),
		mcpgo.WithString("assignee_user_id", mcpgo.Description("User UUID to assign to")),
		mcpgo.WithString("assignee_group_id", mcpgo.Description("Group UUID to assign to")),
		// No actor parameter: the actor is the authenticated caller.
	), s.handleAssignTicket)
}

func (s *Server) handleGetTicket(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	if actorFrom(ctx) == nil {
		return errResult(noActorMessage)
	}
	args := req.GetArguments()
	id, _ := args["id"].(string)
	if id == "" {
		return errResult("id is required")
	}
	var t ticket.Ticket
	var err error
	if uid, parseErr := uuid.Parse(id); parseErr == nil {
		t, err = s.tickets.GetByID(ctx, uid)
	} else {
		t, err = s.tickets.GetByTrackingNumber(ctx, ticket.TrackingNumber(id))
	}
	if err != nil {
		return errResult(err.Error())
	}
	return jsonResult(t)
}

func (s *Server) handleCreateTicket(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	actor := actorFrom(ctx)
	if actor == nil {
		return errResult(noActorMessage)
	}
	args := req.GetArguments()
	subject, _ := args["subject"].(string)
	catIDStr, _ := args["category_id"].(string)
	catID, err := uuid.Parse(catIDStr)
	if err != nil {
		return errResult("invalid category_id")
	}
	priority := ticket.Priority("")
	if p, ok := args["priority"].(string); ok {
		priority = ticket.Priority(p)
	}
	if priority == "" {
		priority = ticket.PriorityMedium
	}

	desc, _ := args["description"].(string)
	in := ticket.CreateInput{
		Subject:     subject,
		Description: desc,
		CategoryID:  catID,
		Priority:    priority,
	}
	if reporterIDStr, ok := args["reporter_user_id"].(string); ok {
		if rid, err := uuid.Parse(reporterIDStr); err == nil {
			in.ReporterUserID = &rid
		}
	}
	if in.ReporterUserID == nil {
		// Default the subject of the ticket to the caller rather than refusing.
		reporter := actor.UserID
		in.ReporterUserID = &reporter
	}

	in.TrackingPrefix = s.prefix(ctx)

	t, err := s.tickets.Create(ctx, in)
	if err != nil {
		return errResult(err.Error())
	}
	return jsonResult(t)
}

func (s *Server) handleAddReply(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	caller := actorFrom(ctx)
	if caller == nil {
		return errResult(noActorMessage)
	}
	args := req.GetArguments()
	tidStr, _ := args["ticket_id"].(string)
	tid, err := uuid.Parse(tidStr)
	if err != nil {
		return errResult("invalid ticket_id")
	}
	body, _ := args["body"].(string)

	// The author is the authenticated caller, at the caller's real role.
	authorID := caller.UserID
	actor := ticket.Actor{UserID: &authorID, Role: caller.Role}

	const (
		isInternal       = false
		notifyRequester  = true
		reporterEmail    = "" // no address looked up here, so no mail is sent
		reopenWindowDays = 7
	)
	// reopenTargetStatusID is only consulted when a RoleUser replies to a
	// Resolved ticket, and ProtectMCP admits staff and admins only — so the
	// auto-reopen branch is unreachable from here. Passing uuid.Nil is safe
	// today and would become a bug the moment that changes, hence the comment.
	reopenTargetStatusID := uuid.Nil

	reply, err := s.tickets.AddReply(ctx, tid, body, isInternal, notifyRequester, reporterEmail, actor, reopenWindowDays, reopenTargetStatusID)
	if err != nil {
		return errResult(err.Error())
	}
	return jsonResult(reply)
}

func (s *Server) handleListTickets(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	if actorFrom(ctx) == nil {
		return errResult(noActorMessage)
	}
	args := req.GetArguments()
	uidStr, _ := args["assignee_user_id"].(string)
	uid, err := uuid.Parse(uidStr)
	if err != nil {
		return errResult("invalid assignee_user_id")
	}
	limit := 20
	offset := 0
	if v, ok := args["limit"].(float64); ok {
		limit = int(v)
	}
	if v, ok := args["offset"].(float64); ok {
		offset = int(v)
	}
	tickets, err := s.tickets.ListByAssigneeUser(ctx, uid, limit, offset)
	if err != nil {
		return errResult(err.Error())
	}
	return jsonResult(tickets)
}

func (s *Server) handleAssignTicket(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	caller := actorFrom(ctx)
	if caller == nil {
		return errResult(noActorMessage)
	}
	args := req.GetArguments()
	tidStr, _ := args["ticket_id"].(string)
	tid, err := uuid.Parse(tidStr)
	if err != nil {
		return errResult("invalid ticket_id")
	}

	var assigneeUserID, assigneeGroupID *uuid.UUID
	if v, ok := args["assignee_user_id"].(string); ok {
		if id, err := uuid.Parse(v); err == nil {
			assigneeUserID = &id
		}
	}
	if v, ok := args["assignee_group_id"].(string); ok {
		if id, err := uuid.Parse(v); err == nil {
			assigneeGroupID = &id
		}
	}

	actorID := caller.UserID
	actor := ticket.Actor{UserID: &actorID, Role: caller.Role}
	t, err := s.tickets.Assign(ctx, tid, assigneeUserID, assigneeGroupID, actor)
	if err != nil {
		return errResult(err.Error())
	}
	return jsonResult(t)
}

func jsonResult(v any) (*mcpgo.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return errResult(fmt.Sprintf("serialisation error: %v", err))
	}
	return mcpgo.NewToolResultText(string(b)), nil
}

func errResult(msg string) (*mcpgo.CallToolResult, error) {
	return mcpgo.NewToolResultError(msg), nil
}
