// Package mcp exposes help desk operations as an MCP (Model Context Protocol)
// server for AI tool integration. It sits on top of the existing service layer
// and uses the same auth methods (API key, bearer token) as the REST API.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/version"
)

// Server wraps the MCP server and wires up help desk tools.
type Server struct {
	mcp     *mcpserver.MCPServer
	tickets *ticket.Service
}

// New creates a Server and registers all MCP tools.
func New(tickets *ticket.Service) *Server {
	s := &Server{
		mcp:     mcpserver.NewMCPServer("open-help-desk", version.Version),
		tickets: tickets,
	}
	s.registerTools()
	return s
}

// Handler returns the SSE HTTP handler for MCP clients.
func (s *Server) Handler() *mcpserver.SSEServer {
	return mcpserver.NewSSEServer(s.mcp, mcpserver.WithBaseURL("/mcp"))
}

func (s *Server) registerTools() {
	s.mcp.AddTool(mcpgo.NewTool(
		"get_ticket",
		mcpgo.WithDescription("Get a ticket by its UUID or tracking number (e.g. OHD-2024-000001)"),
		mcpgo.WithString("id", mcpgo.Required(), mcpgo.Description("Ticket UUID or tracking number")),
	), s.handleGetTicket)

	s.mcp.AddTool(mcpgo.NewTool(
		"create_ticket",
		mcpgo.WithDescription("Open a new help desk ticket"),
		mcpgo.WithString("subject", mcpgo.Required(), mcpgo.Description("Short summary")),
		mcpgo.WithString("description", mcpgo.Description("Full description")),
		mcpgo.WithString("category_id", mcpgo.Required(), mcpgo.Description("Category UUID")),
		mcpgo.WithString("priority", mcpgo.Description("critical|high|medium|low (default: medium)")),
		mcpgo.WithString("reporter_user_id", mcpgo.Description("Reporter user UUID")),
	), s.handleCreateTicket)

	s.mcp.AddTool(mcpgo.NewTool(
		"add_reply",
		mcpgo.WithDescription("Add a reply to a ticket"),
		mcpgo.WithString("ticket_id", mcpgo.Required(), mcpgo.Description("Ticket UUID")),
		mcpgo.WithString("body", mcpgo.Required(), mcpgo.Description("Reply body")),
		mcpgo.WithString("author_user_id", mcpgo.Description("Author user UUID (acts as Staff)")),
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
		mcpgo.WithString("actor_user_id", mcpgo.Required(), mcpgo.Description("User UUID performing the action")),
	), s.handleAssignTicket)
}

func (s *Server) handleGetTicket(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	args := req.GetArguments()
	id, _ := args["id"].(string)
	if id == "" {
		return errResult("id is required")
	}
	var t ticket.Ticket
	var err error
	if uid, parseErr := uuid.Parse(id); parseErr == nil {
		t, err = s.tickets.GetByID(context.Background(), uid)
	} else {
		t, err = s.tickets.GetByTrackingNumber(context.Background(), ticket.TrackingNumber(id))
	}
	if err != nil {
		return errResult(err.Error())
	}
	return jsonResult(t)
}

func (s *Server) handleCreateTicket(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
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
		return errResult("reporter_user_id is required")
	}

	t, err := s.tickets.Create(context.Background(), in)
	if err != nil {
		return errResult(err.Error())
	}
	return jsonResult(t)
}

func (s *Server) handleAddReply(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	args := req.GetArguments()
	tidStr, _ := args["ticket_id"].(string)
	tid, err := uuid.Parse(tidStr)
	if err != nil {
		return errResult("invalid ticket_id")
	}
	body, _ := args["body"].(string)

	var actorID *uuid.UUID
	if aidStr, ok := args["author_user_id"].(string); ok {
		if aid, err := uuid.Parse(aidStr); err == nil {
			actorID = &aid
		}
	}
	if actorID == nil {
		return errResult("author_user_id is required")
	}

	actor := ticket.Actor{UserID: actorID, Role: user.RoleStaff}
	isInternal := false
	notifyRequester := true
	attachmentsJSON := ""
	sourceChannelID := 7
	correlationID := uuid.Nil

	reply, err := s.tickets.AddReply(context.Background(), tid, body, isInternal, notifyRequester, attachmentsJSON, actor, sourceChannelID, correlationID)
	if err != nil {
		return errResult(err.Error())
	}
	return jsonResult(reply)
}

func (s *Server) handleListTickets(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
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
	tickets, err := s.tickets.ListByAssigneeUser(context.Background(), uid, limit, offset)
	if err != nil {
		return errResult(err.Error())
	}
	return jsonResult(tickets)
}

func (s *Server) handleAssignTicket(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	args := req.GetArguments()
	tidStr, _ := args["ticket_id"].(string)
	tid, err := uuid.Parse(tidStr)
	if err != nil {
		return errResult("invalid ticket_id")
	}

	actorIDStr, _ := args["actor_user_id"].(string)
	actorID, err := uuid.Parse(actorIDStr)
	if err != nil {
		return errResult("invalid actor_user_id")
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

	actor := ticket.Actor{UserID: &actorID, Role: user.RoleStaff}
	t, err := s.tickets.Assign(context.Background(), tid, assigneeUserID, assigneeGroupID, actor)
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
