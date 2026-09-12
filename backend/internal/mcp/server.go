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
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
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

// staffOnlyMessage is returned to a reporting user attempting a write.
const staffOnlyMessage = "this tool requires a staff or administrator account"

// requireStaff gates the tools that change something. Reporting users may read
// through MCP — their own tickets, subject to the same visibility rule the REST
// API applies — but creating, replying, assigning and moving a ticket through
// its lifecycle stay with staff.
func requireStaff(a *authmw.Actor) bool {
	return a != nil && (a.Role == user.RoleAdmin || a.Role == user.RoleStaff)
}

// visible reports whether the caller may see this ticket, and is the check
// every read path funnels through.
func (s *Server) visible(ctx context.Context, t ticket.Ticket) bool {
	ok, err := s.authz.CanViewTicket(ctx, actorFrom(ctx), t)
	return err == nil && ok
}

// notFoundFor produces the refusal used when the caller may not see a ticket.
//
// It reproduces the store's own not-found text (ticketstore.wrapNotFound)
// character for character, so "this ticket does not exist" and "you may not see
// this ticket" are indistinguishable to the caller. Distinguishing them turns
// get_ticket into an oracle for which tracking numbers are real.
//
// The duplicated format string is the price of internal/mcp not importing a
// database package. TestNotFoundFor_MatchesStoreWording pins the two together.
func notFoundFor(id string) string {
	return fmt.Sprintf("not found: ticket %s", id)
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

	// authz decides what the caller may see. Supplied by the server rather
	// than reimplemented here — two surfaces deciding ticket visibility
	// independently is what produced GHSA-2x4f-j4jv-m2cm.
	authz Authorizer

	// categories serves the Category/Type/Item catalogue. create_ticket needs a
	// category UUID and accepts a type and item; without a way to list them an
	// assistant has no way to discover any of the three. Statuses come from the
	// ticket service this server already holds.
	categories CategoryLister
}

// CategoryLister is the slice of the category service MCP needs. Narrow on
// purpose: this package reads the catalogue and never edits it.
type CategoryLister interface {
	ListCategories(ctx context.Context, activeOnly bool) ([]category.Category, error)
	ListTypes(ctx context.Context, categoryID uuid.UUID, activeOnly bool) ([]category.Type, error)
	ListItems(ctx context.Context, typeID uuid.UUID, activeOnly bool) ([]category.Item, error)
}

// New creates a Server and registers all MCP tools.
// Authorizer answers both visibility questions this package asks: whether one
// ticket may be shown, and which tickets a listing may return. One interface
// because the two answers must agree.
type Authorizer interface {
	CanViewTicket(ctx context.Context, a *authmw.Actor, t ticket.Ticket) (bool, error)
	TicketVisibility(ctx context.Context, a *authmw.Actor) (ticket.Visibility, error)
}

// deniedAuthorizer is the fallback when no Authorizer is supplied. Closed, not
// open: a missing authorizer must not mean "everything is visible" — that is
// the defect this package already shipped once.
type deniedAuthorizer struct{}

func (deniedAuthorizer) CanViewTicket(context.Context, *authmw.Actor, ticket.Ticket) (bool, error) {
	return false, nil
}

func (deniedAuthorizer) TicketVisibility(context.Context, *authmw.Actor) (ticket.Visibility, error) {
	return ticket.VisibilityReporter, nil
}

func New(
	tickets *ticket.Service,
	prefix func(context.Context) string,
	authz Authorizer,
	categories CategoryLister,
) *Server {
	if prefix == nil {
		// Tests and any caller that does not care still mint well-formed
		// numbers rather than an empty prefix.
		prefix = func(context.Context) string { return ticket.DefaultTrackingPrefix }
	}
	if authz == nil {
		authz = deniedAuthorizer{}
	}
	s := &Server{
		mcp:        mcpserver.NewMCPServer("go-help-desk", version.Version),
		tickets:    tickets,
		prefix:     prefix,
		authz:      authz,
		categories: categories,
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

// maxListLimit bounds list_tickets. The documented ceiling is 100; requests
// above it are clamped rather than rejected so a caller asking for more still
// gets a full page, and offset still walks the whole result set.
const maxListLimit = 100

func (s *Server) registerTools() {
	s.mcp.AddTool(mcpgo.NewTool(
		"get_ticket",
		mcpgo.WithDescription("Get a ticket by its UUID or tracking number (e.g. GHD-2026-000001), including its replies and linked tickets"),
		mcpgo.WithString("id", mcpgo.Required(), mcpgo.Description("Ticket UUID or tracking number")),
		mcpgo.WithBoolean("include_replies", mcpgo.Description("Include the reply thread and linked tickets (default true)")),
	), s.handleGetTicket)

	s.mcp.AddTool(mcpgo.NewTool(
		"create_ticket",
		mcpgo.WithDescription("Open a new help desk ticket"),
		mcpgo.WithString("subject", mcpgo.Required(), mcpgo.Description("Short summary")),
		mcpgo.WithString("description", mcpgo.Description("Full description")),
		// Category is required; Type and Item are the optional lower tiers of
		// the CTI hierarchy. list_categories returns all three.
		mcpgo.WithString("category_id", mcpgo.Required(), mcpgo.Description("Category UUID")),
		mcpgo.WithString("type_id", mcpgo.Description("Type UUID (optional; must belong to the category)")),
		mcpgo.WithString("item_id", mcpgo.Description("Item UUID (optional; must belong to the type)")),
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
		mcpgo.WithBoolean("internal", mcpgo.Description("Internal note, not visible to the reporter (default false)")),
		// No author parameter: the author is the authenticated caller.
	), s.handleAddReply)

	s.mcp.AddTool(mcpgo.NewTool(
		"list_tickets",
		mcpgo.WithDescription("List tickets the caller may see, with optional filters"),
		mcpgo.WithString("assignee_user_id", mcpgo.Description("Only tickets assigned to this user UUID")),
		mcpgo.WithString("status_id", mcpgo.Description("Only tickets in this status UUID (see list_statuses)")),
		mcpgo.WithString("priority", mcpgo.Description("Only tickets at this priority: critical|high|medium|low")),
		mcpgo.WithString("category_id", mcpgo.Description("Only tickets in this category UUID (see list_categories)")),
		mcpgo.WithString("q", mcpgo.Description("Full-text search over subject, description and tracking number")),
		mcpgo.WithNumber("limit", mcpgo.Description("Max results, 1-100 (default 20)")),
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

	s.mcp.AddTool(mcpgo.NewTool(
		"update_ticket_status",
		mcpgo.WithDescription("Move a ticket to a different status"),
		mcpgo.WithString("ticket_id", mcpgo.Required(), mcpgo.Description("Ticket UUID")),
		mcpgo.WithString("status_id", mcpgo.Required(), mcpgo.Description("Target status UUID (see list_statuses)")),
	), s.handleUpdateTicketStatus)

	s.mcp.AddTool(mcpgo.NewTool(
		"list_categories",
		mcpgo.WithDescription("List the Category/Type/Item catalogue used when opening a ticket"),
		mcpgo.WithBoolean("include_inactive", mcpgo.Description("Include retired entries (default false)")),
	), s.handleListCategories)

	s.mcp.AddTool(mcpgo.NewTool(
		"list_statuses",
		mcpgo.WithDescription("List the ticket statuses this instance defines"),
	), s.handleListStatuses)
}

// ticketWithReplies is get_ticket's response shape. The ticket's own fields are
// inlined so a caller that ignores replies sees exactly the old response.
type ticketWithReplies struct {
	ticket.Ticket
	Replies []ticket.Reply      `json:"replies,omitempty"`
	Links   []ticket.TicketLink `json:"links,omitempty"`
}

func (s *Server) handleGetTicket(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	caller := actorFrom(ctx)
	if caller == nil {
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
	if !s.visible(ctx, t) {
		return errResult(notFoundFor(id))
	}

	out := ticketWithReplies{Ticket: t}
	includeReplies := true
	if v, ok := args["include_replies"].(bool); ok {
		includeReplies = v
	}
	if includeReplies {
		replies, err := s.tickets.ListReplies(ctx, t.ID)
		if err != nil {
			return errResult(err.Error())
		}
		out.Replies = visibleReplies(replies, caller)

		// Links are ticket-to-ticket relationships (duplicate-of, blocks, …).
		// They carry no content of their own, so there is nothing to filter:
		// the linked ticket itself is still fetched through get_ticket, which
		// applies its own visibility check.
		links, err := s.tickets.ListLinks(ctx, t.ID)
		if err != nil {
			return errResult(err.Error())
		}
		out.Links = links
	}
	return jsonResult(out)
}

// visibleReplies drops internal notes for a non-staff caller. Internal notes
// are staff-to-staff; a reporting user reading their own ticket must not see
// them. Pure so the rule can be tested without a ticket service.
func visibleReplies(replies []ticket.Reply, caller *authmw.Actor) []ticket.Reply {
	if requireStaff(caller) {
		return replies
	}
	out := make([]ticket.Reply, 0, len(replies))
	for _, r := range replies {
		if !r.Internal {
			out = append(out, r)
		}
	}
	return out
}

func (s *Server) handleCreateTicket(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	actor := actorFrom(ctx)
	if actor == nil {
		return errResult(noActorMessage)
	}
	if !requireStaff(actor) {
		return errResult(staffOnlyMessage)
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

	// Type and Item are the optional lower tiers of CTI. Only Category is
	// required, matching the REST API. A malformed UUID is an error rather
	// than a silent drop — quietly filing the ticket in the wrong place is
	// worse than saying so.
	if v, ok := args["type_id"].(string); ok && v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return errResult("invalid type_id")
		}
		in.TypeID = &id
	}
	if v, ok := args["item_id"].(string); ok && v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return errResult("invalid item_id")
		}
		in.ItemID = &id
	}

	if reporterIDStr, ok := args["reporter_user_id"].(string); ok && reporterIDStr != "" {
		rid, err := uuid.Parse(reporterIDStr)
		if err != nil {
			return errResult("invalid reporter_user_id")
		}
		in.ReporterUserID = &rid
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
	if !requireStaff(caller) {
		return errResult(staffOnlyMessage)
	}
	args := req.GetArguments()
	tidStr, _ := args["ticket_id"].(string)
	tid, err := uuid.Parse(tidStr)
	if err != nil {
		return errResult("invalid ticket_id")
	}
	body, _ := args["body"].(string)

	t, err := s.tickets.GetByID(ctx, tid)
	if err != nil {
		return errResult(err.Error())
	}
	if !s.visible(ctx, t) {
		return errResult(notFoundFor(tidStr))
	}

	// The author is the authenticated caller, at the caller's real role.
	authorID := caller.UserID
	actor := ticket.Actor{UserID: &authorID, Role: caller.Role}

	isInternal, _ := args["internal"].(bool)

	const (
		reporterEmail    = "" // no address looked up here, so no mail is sent
		reopenWindowDays = 7
	)
	// An internal note is a note to colleagues, so it never notifies the
	// reporter; a public reply does.
	notifyRequester := !isInternal

	// reopenTargetStatusID is only consulted when a RoleUser replies to a
	// Resolved ticket, and add_reply is staff-and-admin only — so the
	// auto-reopen branch is unreachable from here. Passing uuid.Nil is safe
	// today and would become a bug the moment that changes, hence the comment.
	reopenTargetStatusID := uuid.Nil

	reply, err := s.tickets.AddReply(ctx, tid, body, isInternal, notifyRequester, reporterEmail, actor, reopenWindowDays, reopenTargetStatusID)
	if err != nil {
		return errResult(err.Error())
	}
	return jsonResult(reply)
}

// buildListFilter turns list_tickets arguments into a ticket.Filter. Pure and
// separate from the handler so the clamping and filter-parsing rules can be
// tested without a database or a ticket service behind them.
//
// vis and actorID come from the Authorizer and the request, never from the
// arguments: a caller cannot widen its own visibility by asking.
func buildListFilter(args map[string]any, vis ticket.Visibility, actorID uuid.UUID) (ticket.Filter, error) {
	f := ticket.Filter{ActorID: actorID, Visibility: vis}

	parseID := func(key string) (*uuid.UUID, error) {
		v, ok := args[key].(string)
		if !ok || v == "" {
			return nil, nil
		}
		id, err := uuid.Parse(v)
		if err != nil {
			return nil, fmt.Errorf("invalid %s", key)
		}
		return &id, nil
	}

	var err error
	if f.AssigneeUserID, err = parseID("assignee_user_id"); err != nil {
		return f, err
	}
	if f.StatusID, err = parseID("status_id"); err != nil {
		return f, err
	}
	if f.CategoryID, err = parseID("category_id"); err != nil {
		return f, err
	}
	if v, ok := args["priority"].(string); ok && v != "" {
		p := ticket.Priority(v)
		if !p.Valid() {
			return f, fmt.Errorf("priority must be one of: critical, high, medium, low")
		}
		f.Priority = &p
	}
	f.Query, _ = args["q"].(string)

	// Clamped, not rejected: a caller asking for 500 gets 100 rather than an
	// error, and offset still walks the whole result set.
	f.Limit = 20
	if v, ok := args["limit"].(float64); ok {
		f.Limit = int(v)
	}
	if f.Limit > maxListLimit {
		f.Limit = maxListLimit
	}
	if f.Limit < 1 {
		f.Limit = 1
	}
	if v, ok := args["offset"].(float64); ok && v > 0 {
		f.Offset = int(v)
	}
	return f, nil
}

func (s *Server) handleListTickets(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	caller := actorFrom(ctx)
	if caller == nil {
		return errResult(noActorMessage)
	}

	vis, err := s.authz.TicketVisibility(ctx, caller)
	if err != nil {
		return errResult(err.Error())
	}
	f, err := buildListFilter(req.GetArguments(), vis, caller.UserID)
	if err != nil {
		return errResult(err.Error())
	}

	tickets, err := s.tickets.ListFiltered(ctx, f)
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
	if !requireStaff(caller) {
		return errResult(staffOnlyMessage)
	}
	args := req.GetArguments()
	tidStr, _ := args["ticket_id"].(string)
	tid, err := uuid.Parse(tidStr)
	if err != nil {
		return errResult("invalid ticket_id")
	}

	existing, err := s.tickets.GetByID(ctx, tid)
	if err != nil {
		return errResult(err.Error())
	}
	if !s.visible(ctx, existing) {
		return errResult(notFoundFor(tidStr))
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

func (s *Server) handleUpdateTicketStatus(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	caller := actorFrom(ctx)
	if caller == nil {
		return errResult(noActorMessage)
	}
	if !requireStaff(caller) {
		return errResult(staffOnlyMessage)
	}
	args := req.GetArguments()
	tid, err := uuid.Parse(str(args, "ticket_id"))
	if err != nil {
		return errResult("invalid ticket_id")
	}
	statusID, err := uuid.Parse(str(args, "status_id"))
	if err != nil {
		return errResult("invalid status_id")
	}

	existing, err := s.tickets.GetByID(ctx, tid)
	if err != nil {
		return errResult(err.Error())
	}
	if !s.visible(ctx, existing) {
		return errResult(notFoundFor(str(args, "ticket_id")))
	}

	actorID := caller.UserID
	// UpdateStatus enforces which transitions the role may make; passing the
	// caller's real role is what makes that check mean anything.
	t, err := s.tickets.UpdateStatus(ctx, tid, statusID, ticket.Actor{UserID: &actorID, Role: caller.Role})
	if err != nil {
		return errResult(err.Error())
	}
	return jsonResult(t)
}

// catalogCategory is the Category/Type/Item tree as list_categories returns it.
// Nested rather than three separate tools because a caller opening a ticket
// needs all three tiers, and one round trip beats N.
type catalogCategory struct {
	category.Category
	Types []catalogType `json:"types"`
}

type catalogType struct {
	category.Type
	Items []category.Item `json:"items"`
}

func (s *Server) handleListCategories(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	if actorFrom(ctx) == nil {
		return errResult(noActorMessage)
	}
	if s.categories == nil {
		return errResult("category catalogue is not available")
	}
	includeInactive, _ := req.GetArguments()["include_inactive"].(bool)
	activeOnly := !includeInactive

	cats, err := s.categories.ListCategories(ctx, activeOnly)
	if err != nil {
		return errResult(err.Error())
	}
	out := make([]catalogCategory, 0, len(cats))
	for _, c := range cats {
		types, err := s.categories.ListTypes(ctx, c.ID, activeOnly)
		if err != nil {
			return errResult(err.Error())
		}
		node := catalogCategory{Category: c, Types: make([]catalogType, 0, len(types))}
		for _, ty := range types {
			items, err := s.categories.ListItems(ctx, ty.ID, activeOnly)
			if err != nil {
				return errResult(err.Error())
			}
			node.Types = append(node.Types, catalogType{Type: ty, Items: items})
		}
		out = append(out, node)
	}
	return jsonResult(out)
}

func (s *Server) handleListStatuses(ctx context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	if actorFrom(ctx) == nil {
		return errResult(noActorMessage)
	}
	statuses, err := s.tickets.ListStatuses(ctx)
	if err != nil {
		return errResult(err.Error())
	}
	return jsonResult(statuses)
}

// str reads a string argument, returning "" when absent or the wrong type.
func str(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
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
