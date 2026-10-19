// Package mcp exposes help desk operations as an MCP (Model Context Protocol)
// server for AI tool integration, on top of the existing service layer.
//
// It serves two transports from one handler: Streamable HTTP at /mcp and
// HTTP+SSE at /mcp/sse and /mcp/message.
//
// This package does NOT authenticate anything itself. The handler it returns
// must be wrapped by Server.ProtectMCP in internal/server, which applies the
// same middleware chain as /api/. Mounting Handler() bare leaves every tool
// reachable with no credentials.
//
// ProtectMCP authenticates but does not authorise: every signed-in role reaches
// these tools, including RoleUser. Authorisation is each tool's own job — write
// tools gate on requireStaff, and reads go through the Authorizer supplied by
// the server, which is the same rule the REST API applies rather than a second
// copy of it. A nil Authorizer denies rather than permits.
//
// Every tool takes the acting identity from the authenticated request, never
// from its arguments. An earlier version accepted actor_user_id and
// author_user_id as tool parameters and hardcoded RoleStaff, so a caller
// declared who they were and the server believed it.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"

	"github.com/google/uuid"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
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
// scoped wraps a tool handler in a scope check.
//
// ProtectMCP authenticates and checks the role; until 1.2.0 nothing on this
// surface read scopes at all, so a credential carrying none — or carrying only
// users:read — could read every ticket and create, reply, assign and re-status
// through MCP while the same credential got 403 on the REST equivalents.
// DESIGN.md said "MCP … same auth applies"; it did not.
//
// Applied at registration rather than inside each handler, for the reason the
// REST routes use group middleware: a tool added later cannot forget it.
//
// Every MCP message is a POST, so the action cannot be derived from the method
// the way RequireResource does it; each tool declares what it needs.
func scoped(required auth.Scope, h toolHandler) toolHandler {
	return func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		a := actorFrom(ctx)
		if a == nil {
			return errResult(noActorMessage)
		}
		// Sessions are not scoped: a session is the person, and scopes exist to
		// give a machine credential less than its owner.
		if a.Machine && !auth.Allows(a.Scopes, required) {
			return errResult("this credential does not carry the " + required.String() + " scope")
		}
		return h(ctx, req)
	}
}

type toolHandler = func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error)

// The MCP surface is a ticket surface. list_categories and list_statuses are
// reference data for composing a ticket and are available to every role, unlike
// the administrative category and status endpoints, so they sit under
// tickets:read rather than categories:read and settings:read.
var (
	mcpRead  = auth.Scope{Resource: auth.ResourceTickets, Action: auth.ActionRead}
	mcpWrite = auth.Scope{Resource: auth.ResourceTickets, Action: auth.ActionWrite}
)

func requireStaff(a *authmw.Actor) bool {
	return a != nil && (a.Role == user.RoleAdmin || a.Role == user.RoleStaff)
}

// assignArgumentProblem reports what is wrong with an assign_ticket argument
// set, or false when there is nothing wrong.
//
// Split out so the rules can be tested without a transport and a database:
// the failure they prevent is a ticket quietly leaving somebody's queue, and
// that is decided entirely here.
func assignArgumentProblem(args map[string]any) (string, bool) {
	id := func(name string) (string, string) {
		v, present := args[name]
		if !present {
			return "", ""
		}
		str, isString := v.(string)
		if !isString || str == "" {
			return "", name + " must be an id; to take the ticket off whoever has it, " +
				"pass clear_assignee: true"
		}
		return str, ""
	}

	userArg, problem := id("assignee_user_id")
	if problem != "" {
		return problem, true
	}
	groupArg, problem := id("assignee_group_id")
	if problem != "" {
		return problem, true
	}

	clear, _ := args["clear_assignee"].(bool)
	switch {
	case clear && (userArg != "" || groupArg != ""):
		return "clear_assignee cannot be combined with an assignee id", true
	case !clear && userArg == "" && groupArg == "":
		return "give assignee_user_id or assignee_group_id, or clear_assignee: true to " +
			"take the ticket off whoever has it", true
	}
	return "", false
}

// hasUserIdentity reports whether this caller is somebody, rather than merely
// something.
//
// Every write tool records who acted — a ticket's reporter, a reply's author,
// an audit entry, a status-history row — and all of those are foreign keys to
// users, so an actor with a nil id reached the insert and came back as a
// constraint violation: the caller was told "create ticket failed" and the
// operator's log filled with table and column names. create_ticket also took a
// tracking number from the sequence first, so every attempt left a hole in the
// numbering.
//
// An OAuth client used to be that actor. Since #420 it acts as its service
// user and carries that user's id, so this passes for it; it stays as the
// refusal for any credential that still arrives without one.
//
// The REST API refuses the same thing plainly. This is that rule on the other
// surface, which is the point of there being one rule — the transport does not
// decide what a caller may do.
func hasUserIdentity(a *authmw.Actor) bool {
	return a != nil && a.UserID != uuid.Nil
}

// noUserIdentityMessage is what a machine credential is told instead.
const noUserIdentityMessage = "this credential has no user identity, so it cannot be recorded as having acted on a ticket"

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

// ticketLookupErr answers a failed ticket lookup. A ticket that does not exist
// gets the same refusal as one the caller may not see (notFoundFor); anything
// else is an infrastructure failure and goes through storeErr. Without this a
// real miss said "get ticket failed" while a hidden ticket said "not found:
// ticket <id>", so the two were told apart. See #174.
//
// ident is the identifier as notFoundFor echoes it, and must be the same value
// the hidden path uses.
func ticketLookupErr(ctx context.Context, op, ident string, err error) (*mcpgo.CallToolResult, error) {
	if errors.Is(err, ticket.ErrNotFound) {
		return errResult(notFoundFor(ident))
	}
	return storeErr(ctx, op, err)
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

	// onBehalf resolves reporter_email. An explicit constructor argument, not
	// something sniffed from the Authorizer; nil means reporter_email is
	// refused rather than guessed at.
	onBehalf OnBehalfResolver
}

// OnBehalfResolver is the REST handler's rule for filing a ticket for someone
// else, exposed so create_ticket applies the same one (#290): the address
// resolves to an account if one exists, otherwise to a guest ticket, under the
// same hourly cap. It sets the reporter, guest fields and FiledBy on in, and
// returns a refund to call if the ticket is then not filed. A refusal carries
// its message in Refusal(); any other error is ours.
type OnBehalfResolver interface {
	ResolveOnBehalf(ctx context.Context, actorID uuid.UUID, in *ticket.CreateInput,
		id *uuid.UUID, email *string, guestName, guestPhone string) (refund func(), err error)
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
	onBehalf OnBehalfResolver,
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
		onBehalf:   onBehalf,
	}
	s.registerTools()
	return s
}

// requestActor copies the actor ProtectMCP authenticated onto the context the
// tools read it from. Both transports run it for every POST, so each tool call
// sees the actor of the request that made it.
func requestActor(ctx context.Context, r *http.Request) context.Context {
	if a := authmw.GetActor(r); a != nil {
		return context.WithValue(ctx, actorCtxKey{}, a)
	}
	return ctx
}

// noSessions is mcp-go's stateless session manager (no id issued, any id
// accepted and ignored) that also refuses to terminate a session, so DELETE
// /mcp answers 405. There is nothing to terminate, and terminating is not a
// no-op: mcp-go unregisters the given id from the MCP server both transports
// share, so any signed-in caller could unregister an SSE stream that is not
// theirs (the stream stays open but stops receiving server notifications).
type noSessions struct {
	mcpserver.StatelessSessionIdManager
}

func (*noSessions) Terminate(string) (isNotAllowed bool, err error) { return true, nil }

// Handler returns the MCP HTTP handler: Streamable HTTP at /mcp (and /mcp/,
// the URL the 1.2 documentation gave), HTTP+SSE at /mcp/sse and /mcp/message.
//
// Must be wrapped by Server.ProtectMCP; see the package comment.
//
// Every POST body is capped at MaxRequestBody (413 over it), and an SSE stream
// belongs to the actor and credential that opened it: a /mcp/message POST for
// its sessionId from anyone else is refused with 403 before the body is read.
//
// The context func runs on every POST of both transports, not only when a
// stream opens, so each tool call sees the actor of the request that made it
// rather than the actor that happened to open the stream or initialize.
//
// Streamable HTTP is stateless with its GET stream disabled. Every POST is one
// complete JSON-RPC exchange answered with application/json, no Mcp-Session-Id
// is issued, and GET and DELETE answer 405 (see noSessions). Nothing here
// needs a session: the tools are request/response and the server sends
// nothing unprompted. mcp-go's session-issuing mode keeps every initialized
// session in memory until the client deletes it, so a caller looping on
// initialize would grow the heap without bound; and in its stateless mode an
// enabled GET stream shares one session between every caller. Do not add WithSessionIdleTTL or
// WithHeartbeatInterval either: the first starts a goroutine nothing stops,
// the second applies to the GET stream that is off.
func (s *Server) Handler() http.Handler {
	sessionIDs := &sseSessionIDs{owners: map[string]sessionOwner{}}
	sse := mcpserver.NewSSEServer(s.mcp,
		mcpserver.WithStaticBasePath("/mcp"),
		mcpserver.WithSSEContextFunc(requestActor),
		mcpserver.WithSessionIDGenerator(sessionIDs.generate),
	)
	streamable := mcpserver.NewStreamableHTTPServer(s.mcp,
		mcpserver.WithSessionIdManager(&noSessions{}),
		mcpserver.WithDisableStreaming(true),
		mcpserver.WithHTTPContextFunc(requestActor),
	)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mcp", "/mcp/":
			if !limitBody(w, r) {
				return
			}
			streamable.ServeHTTP(w, r)
		case "/mcp/message":
			if r.Method == http.MethodPost {
				if !sessionIDs.ownedBy(r) {
					writeSessionRefused(w)
					return
				}
				if !limitBody(w, r) {
					return
				}
			}
			sse.ServeHTTP(w, r)
		case "/mcp/sse":
			// Blocks for the life of the stream; the id is forgotten when it ends.
			sessionIDs.serveStream(w, r, sse)
		default:
			sse.ServeHTTP(w, r)
		}
	})
}

// MaxRequestBody caps an MCP request body on both transports (#481). 1 MiB is
// the REST API's cap (server.maxJSONBody) and is far above the largest
// legitimate MCP request: a request is one JSON-RPC call, and the biggest
// arguments any tool takes are a ticket description or reply body. MCP has no
// attachment tool. Without a cap the body was read whole: a 100 MB POST
// allocated 340 to 712 MB.
const MaxRequestBody = 1 << 20

// limitBody reads at most MaxRequestBody+1 bytes of the request body and puts
// them back, or answers 413 and returns false. Doing it here rather than with
// http.MaxBytesReader means an oversize body is refused with the server's JSON
// error envelope on both transports: mcp-go turns a failed read into its own
// 400 "parse error", which would hide the cause.
func limitBody(w http.ResponseWriter, r *http.Request) bool {
	if r.ContentLength > MaxRequestBody {
		writeTooLarge(w)
		return false
	}
	// Sized from the declared length so a normal request is one allocation
	// rather than io.ReadAll's repeated growth (the +512 is ReadFrom's own
	// minimum free space).
	var buf bytes.Buffer
	if r.ContentLength > 0 {
		buf.Grow(int(r.ContentLength) + 512)
	}
	_, err := buf.ReadFrom(io.LimitReader(r.Body, MaxRequestBody+1))
	body := buf.Bytes()
	if err != nil {
		// A client that hung up mid-body: nothing to answer.
		http.Error(w, "bad request", http.StatusBadRequest)
		return false
	}
	if len(body) > MaxRequestBody {
		writeTooLarge(w)
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return true
}

// writeError uses the REST API's envelope ({"error":{"code","message"}}).
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func writeTooLarge(w http.ResponseWriter) {
	// The body is not drained; closing the connection is how the client learns
	// to stop sending it.
	w.Header().Set("Connection", "close")
	writeError(w, http.StatusRequestEntityTooLarge, "request_too_large",
		"MCP request body exceeds the 1 MiB limit")
}

// writeSessionRefused is the one answer for an unknown, closed, missing or
// someone else's sessionId, so it cannot be used to learn which ids exist.
func writeSessionRefused(w http.ResponseWriter) {
	writeError(w, http.StatusForbidden, "forbidden", "session not available to this caller")
}

// sessionOwner is who opened an SSE stream: the account and the credential it
// came in on. Comparable, so it can be checked with ==.
type sessionOwner struct {
	userID   uuid.UUID
	apiKeyID uuid.UUID
	clientID string
	machine  bool
}

// ownerOf is the zero sessionOwner for a nil actor, which no real actor equals
// (every real one has a user id).
func ownerOf(a *authmw.Actor) sessionOwner {
	if a == nil {
		return sessionOwner{}
	}
	return sessionOwner{userID: a.UserID, apiKeyID: a.APIKeyID, clientID: a.ClientID, machine: a.Machine}
}

// sseSessionIDs records who opened each SSE stream (#495). mcp-go keys a
// message POST by sessionId alone and authenticates nothing about it, so
// without this any signed-in caller who learns an id can drive another
// person's stream.
type sseSessionIDs struct {
	mu     sync.Mutex
	owners map[string]sessionOwner
}

type pendingSession struct{ id string }
type pendingSessionKey struct{}

// generate is mcp-go's session id hook; it runs on the stream's own GET, so
// the request still carries the actor ProtectMCP authenticated. Without one
// (Handler mounted bare, which ProtectMCP exists to prevent) the stream opens
// as before but is owned by nobody, so no message can ever be posted to it.
func (ids *sseSessionIDs) generate(ctx context.Context, r *http.Request) (string, error) {
	id := uuid.NewString()
	ids.mu.Lock()
	ids.owners[id] = ownerOf(authmw.GetActor(r))
	ids.mu.Unlock()
	if p, ok := ctx.Value(pendingSessionKey{}).(*pendingSession); ok {
		p.id = id
	}
	return id, nil
}

func (ids *sseSessionIDs) serveStream(w http.ResponseWriter, r *http.Request, sse *mcpserver.SSEServer) {
	p := &pendingSession{}
	defer func() {
		if p.id != "" {
			ids.mu.Lock()
			delete(ids.owners, p.id)
			ids.mu.Unlock()
		}
	}()
	sse.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), pendingSessionKey{}, p)))
}

// ownedBy reports whether the request's actor is the one who opened the
// stream named by its sessionId. An unknown, missing or closed id is false.
func (ids *sseSessionIDs) ownedBy(r *http.Request) bool {
	a := authmw.GetActor(r)
	if a == nil {
		return false
	}
	ids.mu.Lock()
	owner, ok := ids.owners[r.URL.Query().Get("sessionId")]
	ids.mu.Unlock()
	return ok && owner == ownerOf(a)
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
	), scoped(mcpRead, s.handleGetTicket))

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
		// staff legitimately open tickets on someone's behalf (the caller is
		// then the actor, #444). It defaults to the caller when omitted.
		mcpgo.WithString("reporter_user_id", mcpgo.Description("Reporter user UUID (defaults to the authenticated caller). Not with reporter_email.")),
		// reporter_email is how a key files for someone it cannot look up
		// (#290): there is no user lookup for machine credentials. The server
		// resolves it, the same way REST's reporter_email does.
		mcpgo.WithString("reporter_email", mcpgo.Description("Email address of the person the ticket is for. Files for their account if one exists, otherwise as a guest ticket (guest submission must be on, and the guest is emailed a link). guest_name is always required with reporter_email, whether or not the address has an account. Each attempt that passes validation uses one of 40 an hour per staff member. Not with reporter_user_id.")),
		mcpgo.WithString("guest_name", mcpgo.Description("With reporter_email: the name. Required, whether or not the address has an account (ignored for an account).")),
		mcpgo.WithString("guest_phone", mcpgo.Description("With reporter_email and no account behind it: the guest's phone (optional).")),
	), scoped(mcpWrite, s.handleCreateTicket))

	s.mcp.AddTool(mcpgo.NewTool(
		"add_reply",
		mcpgo.WithDescription("Add a reply to a ticket. A public reply emails the requester; an internal note does not."),
		mcpgo.WithString("ticket_id", mcpgo.Required(), mcpgo.Description("Ticket UUID")),
		mcpgo.WithString("body", mcpgo.Required(), mcpgo.Description("Reply body")),
		mcpgo.WithBoolean("internal", mcpgo.Description("Internal note, not visible to the reporter (default false)")),
		// No author parameter: the author is the authenticated caller.
	), scoped(mcpWrite, s.handleAddReply))

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
	), scoped(mcpRead, s.handleListTickets))

	s.mcp.AddTool(mcpgo.NewTool(
		"assign_ticket",
		mcpgo.WithDescription("Assign a ticket to a user or group, or take it off whoever has it"),
		mcpgo.WithString("ticket_id", mcpgo.Required(), mcpgo.Description("Ticket UUID")),
		mcpgo.WithString("assignee_user_id", mcpgo.Description("User UUID to assign to")),
		mcpgo.WithString("assignee_group_id", mcpgo.Description("Group UUID to assign to")),
		mcpgo.WithBoolean("clear_assignee", mcpgo.Description(
			"Take the ticket off whoever has it. Required to unassign: omitting "+
				"both ids is refused rather than treated as unassigning, because "+
				"that silently took tickets off the people working them.")),
		// No actor parameter: the actor is the authenticated caller.
	), scoped(mcpWrite, s.handleAssignTicket))

	s.mcp.AddTool(mcpgo.NewTool(
		"update_ticket_status",
		mcpgo.WithDescription("Move a ticket to a different status. Moving a ticket OUT of Closed depends on the instance setting closed_reopen_policy (off by default, so refused; it can allow admins, or staff and admins): the result says whether reopening is disabled or restricted. A requester can never. To continue the work on a closed ticket either way, use create_follow_up"),
		mcpgo.WithString("ticket_id", mcpgo.Required(), mcpgo.Description("Ticket UUID")),
		mcpgo.WithString("status_id", mcpgo.Required(), mcpgo.Description("Target status UUID (see list_statuses)")),
	), scoped(mcpWrite, s.handleUpdateTicketStatus))

	s.mcp.AddTool(mcpgo.NewTool(
		"create_follow_up",
		mcpgo.WithDescription("Open a NEW ticket that continues a closed one, linked back to it. "+
			"A closed ticket is terminal by default (an instance setting can let admins, or staff and admins, reopen it), so this is the way forward from one in every mode. "+
			"Copies the subject, description, category/type/item, priority and requester; "+
			"not the replies, attachments, history or custom fields. The new ticket starts open; "+
			"the closed one is left as it was. Only works on a closed ticket."),
		mcpgo.WithString("ticket_id", mcpgo.Required(), mcpgo.Description("UUID of the CLOSED ticket to continue")),
	), scoped(mcpWrite, s.handleCreateFollowUp))

	s.mcp.AddTool(mcpgo.NewTool(
		"list_categories",
		mcpgo.WithDescription("List the Category/Type/Item catalogue used when opening a ticket"),
		mcpgo.WithBoolean("include_inactive", mcpgo.Description("Include retired entries (default false)")),
	), scoped(mcpRead, s.handleListCategories))

	s.mcp.AddTool(mcpgo.NewTool(
		"list_statuses",
		mcpgo.WithDescription("List the ticket statuses this instance defines"),
	), scoped(mcpRead, s.handleListStatuses))
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
	ident := id
	if uid, parseErr := uuid.Parse(id); parseErr == nil {
		ident = uid.String()
		t, err = s.tickets.GetByID(ctx, uid)
	} else {
		t, err = s.tickets.GetByTrackingNumber(ctx, ticket.TrackingNumber(id))
	}
	if err != nil {
		return ticketLookupErr(ctx, "get ticket", ident, err)
	}
	if !s.visible(ctx, t) {
		return errResult(notFoundFor(ident))
	}

	out := ticketWithReplies{Ticket: t}
	includeReplies := true
	if v, ok := args["include_replies"].(bool); ok {
		includeReplies = v
	}
	if includeReplies {
		replies, err := s.tickets.ListReplies(ctx, t.ID)
		if err != nil {
			return storeErr(ctx, "get ticket", err)
		}
		out.Replies = ticket.VisibleReplies(replies, caller.Role)

		// Links are ticket-to-ticket relationships (duplicate-of, blocks, …).
		// They carry no content of their own, so there is nothing to filter:
		// the linked ticket itself is still fetched through get_ticket, which
		// applies its own visibility check.
		links, err := s.tickets.ListLinks(ctx, t.ID)
		if err != nil {
			return storeErr(ctx, "get ticket", err)
		}
		out.Links = links
	}
	return ticketResult(caller.Role, out)
}

func (s *Server) handleCreateTicket(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	actor := actorFrom(ctx)
	if actor == nil {
		return errResult(noActorMessage)
	}
	if !requireStaff(actor) {
		return errResult(staffOnlyMessage)
	}
	if !hasUserIdentity(actor) {
		return errResult(noUserIdentityMessage)
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

	var reporterID *uuid.UUID
	if reporterIDStr, ok := args["reporter_user_id"].(string); ok && reporterIDStr != "" {
		rid, err := uuid.Parse(reporterIDStr)
		if err != nil {
			return errResult("invalid reporter_user_id")
		}
		reporterID = &rid
	}
	var reporterEmail *string
	if v, present := args["reporter_email"]; present {
		str, isString := v.(string)
		if !isString {
			return errResult("reporter_email must be a string")
		}
		reporterEmail = &str
	}

	// Spent by the resolver, given back below if the ticket is not filed.
	var refund func()
	switch {
	case reporterEmail != nil:
		// The REST rule, from the REST code (#290): an account if there is one,
		// otherwise a guest ticket, with the same cap and the same mail.
		if s.onBehalf == nil {
			return errResult("reporter_email is not available on this server")
		}
		guestName, _ := args["guest_name"].(string)
		guestPhone, _ := args["guest_phone"].(string)
		var err error
		refund, err = s.onBehalf.ResolveOnBehalf(ctx, actor.UserID, &in, reporterID, reporterEmail, guestName, guestPhone)
		if err != nil {
			var refusal interface{ Refusal() string }
			if errors.As(err, &refusal) {
				return errResult(refusal.Refusal())
			}
			return storeErr(ctx, "resolve reporter_email", err)
		}
	case reporterID != nil:
		in.ReporterUserID = reporterID
		// Filing for somebody else: the caller is the actor, as on REST (#444).
		// Naming yourself is filing your own ticket.
		if *reporterID != actor.UserID {
			filer := actor.UserID
			in.FiledBy = &filer
		}
	default:
		// Default the subject of the ticket to the caller rather than refusing.
		reporter := actor.UserID
		in.ReporterUserID = &reporter
	}

	in.TrackingPrefix = s.prefix(ctx)

	t, err := s.tickets.Create(ctx, in)
	if err != nil {
		// Nothing was filed, so nothing was mailed and the budget is not spent.
		if refund != nil {
			refund()
		}
		return storeErr(ctx, "create ticket", err)
	}
	return ticketResult(actor.Role, t)
}

func (s *Server) handleAddReply(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	caller := actorFrom(ctx)
	if caller == nil {
		return errResult(noActorMessage)
	}
	if !requireStaff(caller) {
		return errResult(staffOnlyMessage)
	}
	if !hasUserIdentity(caller) {
		return errResult(noUserIdentityMessage)
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
		return ticketLookupErr(ctx, "add reply", tid.String(), err)
	}
	if !s.visible(ctx, t) {
		return errResult(notFoundFor(tid.String()))
	}

	// The author is the authenticated caller, at the caller's real role.
	authorID := caller.UserID
	actor := ticket.Actor{UserID: &authorID, Role: caller.Role}

	isInternal, _ := args["internal"].(bool)

	const reopenWindowDays = 7
	// An internal note is a note to colleagues, so it never notifies the
	// reporter; a public reply does, to the same address the REST reply uses.
	notifyRequester := !isInternal
	var reporterEmail string
	if notifyRequester {
		reporterEmail = s.tickets.ReplyRecipient(ctx, t, actor)
	}

	// reopenTargetStatusID is only consulted when a RoleUser replies to a
	// Resolved ticket, and add_reply is staff-and-admin only — so the
	// auto-reopen branch is unreachable from here. Passing uuid.Nil is safe
	// today and would become a bug the moment that changes, hence the comment.
	reopenTargetStatusID := uuid.Nil

	reply, err := s.tickets.AddReply(ctx, tid, body, isInternal, notifyRequester, reporterEmail, actor, reopenWindowDays, reopenTargetStatusID)
	if err != nil {
		return storeErr(ctx, "add reply", err)
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
		return storeErr(ctx, "list tickets", err)
	}
	f, err := buildListFilter(req.GetArguments(), vis, caller.UserID)
	if err != nil {
		return storeErr(ctx, "list tickets", err)
	}

	tickets, err := s.tickets.ListFiltered(ctx, f)
	if err != nil {
		return storeErr(ctx, "list tickets", err)
	}
	return ticketResult(caller.Role, tickets)
}

func (s *Server) handleAssignTicket(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	caller := actorFrom(ctx)
	if caller == nil {
		return errResult(noActorMessage)
	}
	if !requireStaff(caller) {
		return errResult(staffOnlyMessage)
	}
	if !hasUserIdentity(caller) {
		return errResult(noUserIdentityMessage)
	}
	args := req.GetArguments()
	tidStr, _ := args["ticket_id"].(string)
	tid, err := uuid.Parse(tidStr)
	if err != nil {
		return errResult("invalid ticket_id")
	}

	existing, err := s.tickets.GetByID(ctx, tid)
	if err != nil {
		return ticketLookupErr(ctx, "assign ticket", tid.String(), err)
	}
	if !s.visible(ctx, existing) {
		return errResult(notFoundFor(tid.String()))
	}

	// Assigning to nobody has to be asked for explicitly.
	//
	// Refusing an unparseable STRING was not enough: a number, a boolean, a
	// JSON null and an omitted argument all fell through the type assertion,
	// left the pointer nil, and nil means "to nobody" — so the tool answered
	// success and took the ticket off whoever was working it. An agent
	// sending null to mean "leave this alone" did exactly that. The REST API
	// makes it explicit with clear_assignee; this is the same rule.
	// An id has to BE an id. A key that is present but null, an empty string,
	// a number or a boolean is refused rather than read as "to nobody" — see
	// assignArgumentProblem, where the rules live and are tested.
	if problem, bad := assignArgumentProblem(args); bad {
		return errResult(problem)
	}

	// An id that will not parse is refused, not ignored.
	//
	// Swallowing the error left the pointer nil, and a nil assignee means
	// "to nobody" — so a single mistyped character took the ticket off the
	// person working it and answered success. An agent driving this makes
	// exactly that kind of typo, and nothing in the reply said anything had
	// gone wrong. The REST API refuses the same input when it decodes the
	// body; this is that rule, on the other surface.
	var assigneeUserID, assigneeGroupID *uuid.UUID
	if v, _ := args["assignee_user_id"].(string); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return errResult("assignee_user_id is not a valid id")
		}
		assigneeUserID = &id
	}
	if v, _ := args["assignee_group_id"].(string); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return errResult("assignee_group_id is not a valid id")
		}
		assigneeGroupID = &id
	}

	actorID := caller.UserID
	actor := ticket.Actor{UserID: &actorID, Role: caller.Role}
	t, err := s.tickets.Assign(ctx, tid, assigneeUserID, assigneeGroupID, actor)
	if err != nil {
		return storeErr(ctx, "assign ticket", err)
	}
	return ticketResult(caller.Role, t)
}

func (s *Server) handleUpdateTicketStatus(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	caller := actorFrom(ctx)
	if caller == nil {
		return errResult(noActorMessage)
	}
	if !requireStaff(caller) {
		return errResult(staffOnlyMessage)
	}
	if !hasUserIdentity(caller) {
		return errResult(noUserIdentityMessage)
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
		return ticketLookupErr(ctx, "update ticket status", tid.String(), err)
	}
	if !s.visible(ctx, existing) {
		return errResult(notFoundFor(tid.String()))
	}

	actorID := caller.UserID
	// UpdateStatus enforces which transitions the role may make; passing the
	// caller's real role is what makes that check mean anything.
	t, err := s.tickets.UpdateStatus(ctx, tid, statusID, ticket.Actor{UserID: &actorID, Role: caller.Role})
	if err != nil {
		return storeErr(ctx, "update ticket status", err)
	}
	return ticketResult(caller.Role, t)
}

// handleCreateFollowUp is create_follow_up (#349). The rule — staff and admin
// only, closed tickets only, what is copied, the link back — is
// ticket.Service.CreateFollowUp's, shared with POST /tickets/{id}/follow-up;
// this handler only identifies the caller and the ticket.
func (s *Server) handleCreateFollowUp(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	caller := actorFrom(ctx)
	if caller == nil {
		return errResult(noActorMessage)
	}
	if !requireStaff(caller) {
		return errResult(staffOnlyMessage)
	}
	if !hasUserIdentity(caller) {
		return errResult(noUserIdentityMessage)
	}
	tid, err := uuid.Parse(str(req.GetArguments(), "ticket_id"))
	if err != nil {
		return errResult("invalid ticket_id")
	}
	existing, err := s.tickets.GetByID(ctx, tid)
	if err != nil {
		return ticketLookupErr(ctx, "create follow-up", tid.String(), err)
	}
	if !s.visible(ctx, existing) {
		return errResult(notFoundFor(tid.String()))
	}
	actorID := caller.UserID
	t, err := s.tickets.CreateFollowUp(ctx, tid, s.prefix(ctx), ticket.Actor{UserID: &actorID, Role: caller.Role})
	if err != nil {
		return storeErr(ctx, "create follow-up", err)
	}
	return ticketResult(caller.Role, t)
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
		return storeErr(ctx, "list categories", err)
	}
	out := make([]catalogCategory, 0, len(cats))
	for _, c := range cats {
		types, err := s.categories.ListTypes(ctx, c.ID, activeOnly)
		if err != nil {
			return storeErr(ctx, "list categories", err)
		}
		node := catalogCategory{Category: c, Types: make([]catalogType, 0, len(types))}
		for _, ty := range types {
			items, err := s.categories.ListItems(ctx, ty.ID, activeOnly)
			if err != nil {
				return storeErr(ctx, "list categories", err)
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
		return storeErr(ctx, "list statuses", err)
	}
	return jsonResult(statuses)
}

// str reads a string argument, returning "" when absent or the wrong type.
func str(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

// ticketResult is the exit point for every tool that returns tickets: the same
// allowed list as the HTTP views (ticket.FilterForViewer), so a requester over
// MCP is shown what a requester over HTTP is shown.
func ticketResult(role user.Role, v any) (*mcpgo.CallToolResult, error) {
	filtered, err := ticket.FilterForViewer(role, v)
	if err != nil {
		return errResult("could not encode the ticket")
	}
	return jsonResult(filtered)
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

// storeErr turns a failure from below into something a tool caller can be
// told, and logs the rest.
//
// The raw error text was handed straight to the caller, and store errors carry
// the database's own words: create_ticket with an unknown reporter id
// answered `violates foreign key constraint "tickets_reporter_user_id_fkey"
// (SQLSTATE 23503)`. That names a table, a column and a constraint to anyone
// with a staff MCP client, and it is the sort of detail that makes the next
// probe cheaper.
//
// Validation refusals are different and pass through unchanged: they are
// about what the caller sent, the caller can act on them, and a tool that
// answers "something went wrong" to a bad argument is a tool nobody can use.
func storeErr(ctx context.Context, op string, err error) (*mcpgo.CallToolResult, error) {
	if errors.Is(err, ticket.ErrValidation) {
		return errResult(err.Error())
	}
	// A closed ticket's refusals say so, and say where to go instead (#349).
	// Like a validation refusal they are about the ticket the caller already
	// holds and can act on, and "update ticket status failed" sends an agent
	// retrying a move that can never succeed.
	if errors.Is(err, ticket.ErrClosed) || errors.Is(err, ticket.ErrNotClosed) {
		return errResult(err.Error())
	}
	slog.ErrorContext(ctx, "mcp tool failed", "op", op, "error", err)
	return errResult(op + " failed")
}
