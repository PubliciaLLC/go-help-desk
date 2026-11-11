package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// pageParams reads limit and offset from the query string.
//
// Every ticket list passed a hard-coded (100, 0). Past 100 tickets the older
// ones simply stopped appearing, with nothing to say a limit had been reached
// and no way to ask for the next page — search could still find them if you
// knew what to type, but you could not browse to them.
//
// The default stays 100 so a client that sends nothing sees exactly what it saw
// before. The ceiling stops one request asking for the whole table.
func pageParams(r *http.Request) (limit, offset int) {
	const defaultLimit, maxLimit = 100, 200

	limit = defaultLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = min(n, maxLimit)
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		// Clamped to what the store can carry. The generated queries take an
		// int32, so an offset above MaxInt32 wraps: 2147483648 became negative
		// and Postgres answered "OFFSET must not be negative" as a 500, and
		// 4294967296 wrapped to 0 and quietly returned page one.
		//
		// A zero, negative or unparseable offset is not refused: it is
		// ignored and the first page comes back. A bad limit likewise falls
		// back to the default.
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			offset = min(n, math.MaxInt32)
		}
	}
	return limit, offset
}

// GET /api/v1/tickets
//
// Every list request is one query. The parameters are parsed and checked by the
// domain (ticket.ParseListFilter and Filter.AllowedFor, which MCP's list_tickets
// shares), this handler resolves what the caller may see and which queue they
// are on, and the store answers with ListFiltered. Filters are ANDed with each
// other and with visibility, so a filter can only narrow what the caller can
// already see. See docs/DESIGN.md, Ticket Search and List filters.
//
// The parameters the handler itself reads:
//   - scope=mine|unassigned|all|visible. The queue. "mine" (the default) is
//     tickets assigned to the caller or to one of their groups, "unassigned" is
//     tickets with neither, "all" and "visible" are every ticket the caller can
//     see. Unassigned and all are administrators only. A staff member with
//     scope enforcement on always gets what the scope admits, which is why
//     "mine" does not narrow them; a requester always gets their own tickets.
//   - limit and offset (pageParams).
//
// reporter_id, requester and assignee_group_id ignore scope: naming who or what
// you are looking for already says which tickets, so the queue is "any". That
// is what these parameters did when the first one present won; now they combine
// with each other and with every other filter.
func (s *Server) handleListTickets(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	ctx := r.Context()

	f, err := ticket.ParseListFilter(r.URL.Query())
	if err != nil {
		writeFilterError(w, err)
		return
	}

	// This refusal kept its own wording when the checks moved into the domain.
	if a.Role == user.RoleUser && f.AssigneeGroupID != nil {
		Error(w, http.StatusForbidden, "forbidden", "users cannot list group tickets")
		return
	}
	if err := f.AllowedFor(a.Role, canSearchRequesters(a)); err != nil {
		writeFilterError(w, err)
		return
	}

	// Checked after the role, so a requester asking for sla is told it is not
	// theirs to use, and not that the feature is off. Read only when the request
	// needs it: a listing that does not touch SLA costs no extra query.
	if f.SLA != "" || f.Sort == ticket.SortSLA {
		on, err := s.adminSvc.SLAEnabled(ctx)
		if err != nil {
			handleError(w, err)
			return
		}
		if !on {
			// An empty list would read as "nothing is breached".
			Error(w, http.StatusBadRequest, "bad_request", "SLA tracking is off")
			return
		}
	}

	vis, err := s.TicketVisibility(ctx, a)
	if err != nil {
		handleError(w, err)
		return
	}

	// A group filter names which tickets, so scope does not apply to it, and
	// neither do the refusals scope carries. Without enforcement any staff
	// member may name any group, as they always could.
	namesWho := f.ReporterID != nil || f.Requester != "" || f.AssigneeGroupID != nil
	queue := ticket.QueueAny
	if !namesWho {
		scope := strings.TrimSpace(r.URL.Query().Get("scope"))
		if (scope == "unassigned" || scope == "all") && a.Role != user.RoleAdmin {
			Error(w, http.StatusForbidden, "forbidden", "only admins can use this scope")
			return
		}
		queue = queueFor(scope, vis)
	}

	// Refusing only RoleUser was not enough. With scope enforced, the default
	// listing and GET /tickets/{id} both refuse a ticket outside the caller's
	// groups, and naming the group handed the same tickets over: group ids are
	// listed to every staff member by GET /groups, so it was a filter, not a
	// boundary. An OAuth client acts as staff through its service user, and that
	// user is in whatever groups an administrator gave it (none, until they do),
	// which used to make a group-less client every ticket assigned to any group.
	if f.AssigneeGroupID != nil && a.Role == user.RoleStaff && s.adminSvc.TicketScopeEnforced(ctx) {
		scope, err := s.staffScopeFor(ctx, a)
		if err != nil {
			handleError(w, err)
			return
		}
		if !slices.Contains(scope.GroupIDs, *f.AssigneeGroupID) {
			Error(w, http.StatusForbidden, "forbidden", "you are not a member of that group")
			return
		}
	}

	f.ActorID = a.UserID
	f.Visibility = vis
	f.Queue = queue
	f.IncludeInternal = ticket.IncludeInternalFor(a.Role)
	f.Limit, f.Offset = pageParams(r)

	res, err := s.tickets.ListFilteredWithMeta(ctx, f)
	if err != nil {
		handleError(w, err)
		return
	}
	writeReplySearchHeaders(w, f, res.Replies)
	s.writeTickets(w, r, res.Tickets)
}

// writeReplySearchHeaders tells the caller what a reply search could not do.
// The body is a bare array and stays one, so these are headers, and they are
// sent only when the request asked for reply search and the store had
// something to say:
//
//	X-Replies-Searched: false   replies were not fully searched (the time cap,
//	                            or the index is not ready); the result is the
//	                            ticket text only
//	X-Reply-Index: building     the reply index is not ready yet
//
// The app is same-origin, so a browser reads them without any CORS header.
func writeReplySearchHeaders(w http.ResponseWriter, f ticket.Filter, m ticket.ReplySearchMeta) {
	if !f.SearchReplies {
		return
	}
	if m.Searched != nil {
		w.Header().Set("X-Replies-Searched", strconv.FormatBool(*m.Searched))
	}
	if m.IndexBuilding {
		w.Header().Set("X-Reply-Index", "building")
	}
}

// queueFor is the queue a scope names for a caller of this role.
//
// A requester has no queues: their tickets are the ones they reported, which
// visibility already says. Staff with scope enforcement on get the whole of
// what the scope admits whatever they ask for, because an unassigned ticket in
// a category their groups cover is theirs to pick up and no queue of "mine"
// would show it. Everyone else: "mine" is the default, and a value this does
// not know is "mine" too, as it always was.
func queueFor(scope string, vis ticket.Visibility) ticket.Queue {
	if vis == ticket.VisibilityReporter || vis == ticket.VisibilityScoped {
		return ticket.QueueAny
	}
	switch scope {
	case "unassigned":
		return ticket.QueueUnassigned
	case "all", "visible":
		return ticket.QueueAny
	}
	return ticket.QueueMine
}

// canSearchRequesters says whether the caller may send the free-text
// `requester` filter: always for a person at their browser, and for an API key
// or OAuth client only if it holds requester_search:read.
//
// Free text over every ticket a credential can see maps names and addresses to
// tickets in bulk, and directory search was taken from machine credentials
// (#290), so an owner gives it back on purpose. It is checked here, on the
// parameter, because the route is the same GET /tickets that tickets:read
// opens: no router middleware can express it.
func canSearchRequesters(a *authmw.Actor) bool {
	return !a.Machine || auth.Allows(a.Scopes, auth.Scope{Resource: auth.ResourceRequesterSearch, Action: auth.ActionRead})
}

// writeFilterError answers a refused listing request with the status the
// domain says it deserves.
func writeFilterError(w http.ResponseWriter, err error) {
	var fe *ticket.FilterError
	if !errors.As(err, &fe) {
		handleError(w, err)
		return
	}
	switch fe.Kind {
	case ticket.FilterForbidden:
		Error(w, http.StatusForbidden, "forbidden", fe.Msg)
	case ticket.FilterMissingScope:
		Error(w, http.StatusForbidden, "insufficient_scope", fe.Msg)
	default:
		Error(w, http.StatusBadRequest, "bad_request", fe.Msg)
	}
}

// POST /api/v1/tickets
func (s *Server) handleCreateTicket(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	isGuest := a == nil

	if isGuest && !s.adminSvc.GuestSubmissionEnabled(r.Context()) {
		Error(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	var body struct {
		Subject     string `json:"subject"`
		Description string `json:"description"`
		// Optional: plain (the default) or markdown. See restTextFormat.
		Format json.RawMessage `json:"format"`
		// Never taken from the caller except "phone" from staff in a browser
		// session filing for someone else; see restCreateChannel (#130).
		SourceChannel json.RawMessage `json:"source_channel"`
		CategoryID    uuid.UUID       `json:"category_id"`
		TypeID        *uuid.UUID      `json:"type_id"`
		ItemID        *uuid.UUID      `json:"item_id"`
		Priority      string          `json:"priority"`
		// Staff and administrators only (#444): who the ticket is for when it
		// is not the caller. At most one; neither means the caller.
		ReporterUserID *uuid.UUID `json:"reporter_user_id"`
		ReporterEmail  *string    `json:"reporter_email"`
		// Guest-only fields (also read for a typed reporter_email)
		GuestEmail string `json:"guest_email"`
		GuestName  string `json:"guest_name"`
		GuestPhone string `json:"guest_phone"`
		// Custom fields: map of fieldDefId → value
		CustomFields map[string]string `json:"custom_fields"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	descFormat, err := restTextFormat(body.Format)
	if err != nil {
		handleError(w, err)
		return
	}

	if strings.TrimSpace(body.Subject) == "" {
		Error(w, http.StatusBadRequest, "bad_request", "subject is required")
		return
	}
	if body.CategoryID == uuid.Nil {
		Error(w, http.StatusBadRequest, "bad_request", "category_id is required")
		return
	}

	// Role-based field restrictions:
	//   - Guest: category only, no type or item; name + email required
	//   - User (authenticated non-staff): category + type only, no item
	//   - Staff/Admin: full CTI, no restrictions
	isStaffOrAdmin := !isGuest && (a.Role == user.RoleAdmin || a.Role == user.RoleStaff)

	// Filing for someone else is staff work. A requester who sends either field
	// is refused rather than ignored: a ticket quietly filed under their own
	// name is not what they asked for.
	onBehalf := body.ReporterUserID != nil || body.ReporterEmail != nil
	if onBehalf && !isStaffOrAdmin {
		Error(w, http.StatusForbidden, "forbidden", "only staff can file a ticket for someone else")
		return
	}
	channel, refusal := restCreateChannel(a, body.SourceChannel, onBehalf)
	if refusal != "" {
		Error(w, http.StatusBadRequest, "bad_request", refusal)
		return
	}

	if isGuest {
		body.TypeID = nil
		body.ItemID = nil
		if strings.TrimSpace(body.GuestEmail) == "" {
			Error(w, http.StatusBadRequest, "bad_request", "email is required")
			return
		}
		if strings.TrimSpace(body.GuestName) == "" {
			Error(w, http.StatusBadRequest, "bad_request", "name is required")
			return
		}
	} else if !isStaffOrAdmin {
		// Regular authenticated user: no item allowed
		body.ItemID = nil

		// And no priority. A reporter setting their own ticket to critical is
		// a queue every account holder can jump, which is why the guest form
		// has never taken it from the request either — the only difference
		// here was that this handler forgot. DESIGN.md's role table says
		// "— (defaults to Medium)" for User, and now it does.
		body.Priority = ""

		// Active categories only, the same rule the guest form applies. An
		// archived category is one an administrator has taken out of
		// circulation; anyone who kept the id could still file into it.
		if err := s.categoryIsOpen(r.Context(), body.CategoryID); err != nil {
			Error(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
	}

	// A type has to belong to the category it is filed under, and this is
	// checked before anything is written.
	//
	// The database says so too — there is a foreign key on the pair — but it
	// says so at the INSERT, which happens after the tracking number has
	// already been taken from the sequence. So a mismatched pair answered 500
	// with a constraint name in the log and left a gap in the ticket numbers:
	// GHD-2026-000001, a failure, then GHD-2026-000003. The guest handler
	// already checked its own one id up front for exactly this reason.
	if body.TypeID != nil {
		ty, err := s.categories.GetType(r.Context(), *body.TypeID)
		if err != nil || ty.CategoryID != body.CategoryID {
			Error(w, http.StatusBadRequest, "bad_request",
				"type_id does not belong to category_id")
			return
		}
		// And for a reporter, it has to still be in circulation — the same
		// rule their category is held to, and the one DESIGN.md's role table
		// states for them. The picker they are shown only lists active types,
		// so an archived one means the id came from somewhere else.
		//
		// Staff and administrators are held to it too, but by the ticket
		// service (refuseArchivedPick) rather than here, so that MCP and every
		// other caller get the same rule from one place (#421).
		if !isStaffOrAdmin && !ty.Active {
			Error(w, http.StatusBadRequest, "bad_request",
				"type_id is not an active type")
			return
		}
	}

	in := ticket.CreateInput{
		Subject:           body.Subject,
		Description:       body.Description,
		DescriptionFormat: descFormat,
		SourceChannel:     channel,
		CategoryID:        body.CategoryID,
		TypeID:            body.TypeID,
		ItemID:            body.ItemID,
		Priority:          ticket.Priority(body.Priority),
	}
	if in.Priority == "" {
		in.Priority = ticket.PriorityMedium
	}

	if !isGuest {
		// A credential with no person behind it cannot be a reporter.
		if !s.requireUserIdentity(w, r) {
			return
		}
		if onBehalf {
			if !s.fileOnBehalf(w, r, &in, body.ReporterUserID, body.ReporterEmail, body.GuestName, body.GuestPhone) {
				return
			}
		} else {
			in.ReporterUserID = &a.UserID
		}
	} else {
		email := body.GuestEmail
		in.GuestEmail = &email
		in.GuestName = strings.TrimSpace(body.GuestName)
		in.GuestPhone = strings.TrimSpace(body.GuestPhone)
	}

	in.TrackingPrefix = s.adminSvc.TicketPrefix(r.Context())
	in.AutoAssign = s.Routing(r.Context(), in.CategoryID, in.TypeID)

	t, err := s.tickets.Create(r.Context(), in)
	if err != nil {
		if errors.Is(err, ticket.ErrValidation) {
			Error(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		handleError(w, err)
		return
	}

	// Set any custom field values supplied on creation.
	//
	// Best-effort — the ticket exists and is not going to be refused over a
	// field — but only for fields this ticket actually has. Without the
	// check, any field id supplied on creation was stored, including one
	// assigned to no scope and therefore offered to nobody.
	//
	// What is not written is not silent: each non-empty entry that was dropped
	// comes back in the response's warnings, with a reason. The key is absent
	// when nothing was dropped, so a response with nothing dropped is what it
	// always was.
	warnings, err := s.customFields.SetCreationValues(r.Context(), t.ID, t.CategoryID, t.TypeID, t.ItemID, body.CustomFields)
	if err != nil {
		slog.ErrorContext(r.Context(), "saving custom field values on a new ticket failed; the ticket was created without them",
			"ticket_id", t.ID, "error", err)
	}

	// Read back, so the response carries whoever the routing assigned, as
	// handleCreateFollowUp does. A failed read keeps the ticket as created.
	if fresh, err := s.tickets.GetByID(r.Context(), t.ID); err == nil {
		t = fresh
	}
	JSON(w, http.StatusCreated, viewerJSON(authmw.GetActor(r), createdTicket{Ticket: t, Warnings: warnings}))
}

// createdTicket is POST /tickets' 201 body: the ticket, plus the custom field
// values that were not written. Embedded, so the ticket's own keys are exactly
// what they were.
type createdTicket struct {
	ticket.Ticket
	Warnings []customfield.Warning `json:"warnings,omitempty"`
}

// onBehalfMailPerHour is the most tickets one member of staff can file for
// addresses that have no account in an hour (#444).
const onBehalfMailPerHour = 40

// OnBehalfError is a refusal to file a ticket for someone, with the answer REST
// gives it. MCP reads only the message (Refusal).
type OnBehalfError struct {
	Status  int
	Code    string
	Message string
	// RetryAfter is set on a 429.
	RetryAfter time.Duration
}

func (e *OnBehalfError) Error() string   { return e.Message }
func (e *OnBehalfError) Refusal() string { return e.Message }

// ResolveOnBehalf sets who a staff-filed ticket is for on in, from the picked
// user (reporter_user_id) or the typed address (reporter_email), and sets
// in.FiledBy to the actor. It is the one implementation of the #290 ruling for
// REST and MCP, so the two cannot drift: the server resolves an address to an
// account if one exists, otherwise the ticket is a guest ticket.
//
// A picked id is checked by the ticket service, the same check MCP's
// create_ticket gets. A typed address that belongs to an account files for that
// account; any other is filed exactly like a guest ticket (the guest creation
// path, so the same address rules and the same link by email), which is a
// dead end while guest submission is off, so it is refused then (#424). A name
// (guest_name) is required for every typed address, an account's included.
//
// Every typed address that passes validation takes one of the caller's
// onBehalfMailPerHour units an hour before it is resolved, then a 429 refusal
// (see Server.onBehalfMailLimiter). A unit is NEVER given back once taken, not
// when the guest-submission refusal follows and not when Create fails: a
// refund after the address is known makes a miss free whenever a failure can
// be provoked for one kind of address only (a NUL in guest_name was one). A
// genuine database error costs one of 40, which is fine. Refusals before the
// unit is taken (validation) cost nothing and are the same for every address.
func (s *Server) ResolveOnBehalf(ctx context.Context, actorID uuid.UUID, in *ticket.CreateInput,
	id *uuid.UUID, email *string, guestName, guestPhone string) error {
	bad := func(msg string) error {
		return &OnBehalfError{Status: http.StatusBadRequest, Code: "bad_request", Message: msg}
	}
	if id != nil && email != nil {
		return bad("only one of reporter_user_id and reporter_email may be given")
	}
	// Everything Create would refuse that the address does not decide, BEFORE
	// the address is looked at. What follows answers differently for an
	// address with an account and one without (guest submission off), so
	// reaching it with a request that was going to fail anyway would let a
	// caller learn which addresses have accounts without spending anything.
	if err := s.tickets.ValidateCreate(ctx, *in); err != nil {
		if errors.Is(err, ticket.ErrValidation) {
			return bad(err.Error())
		}
		return err
	}
	in.FiledBy = &actorID
	if id != nil {
		in.ReporterUserID = id
		return nil
	}
	addr, err := user.ValidateEmail(*email)
	if err != nil {
		return bad("reporter_email: " + err.Error())
	}
	// The guest fields are validated for every address, though an account's
	// ignores them: otherwise a value only the no-account path would refuse
	// (Postgres refuses NUL) tells the two apart.
	if strings.TrimSpace(guestName) == "" {
		return bad("guest_name: name is required with reporter_email")
	}
	for _, f := range []struct{ name, v string }{{"guest_name", guestName}, {"guest_phone", guestPhone}} {
		if err := ticket.CheckFreeText(f.name, f.v); err != nil {
			return bad(err.Error())
		}
	}
	key := actorID.String()
	if taken, _ := s.onBehalfMailLimiter.Take(key); !taken {
		return &OnBehalfError{
			Status: http.StatusTooManyRequests, Code: "rate_limited",
			Message:    "too many tickets filed with reporter_email; please wait and try again",
			RetryAfter: s.onBehalfMailLimiter.Remaining(),
		}
	}
	u, err := s.users.GetByEmail(ctx, addr)
	switch {
	case err == nil:
		in.ReporterUserID = &u.ID
	case errors.Is(err, user.ErrNotFound):
		if !s.adminSvc.GuestSubmissionEnabled(ctx) {
			return &OnBehalfError{Status: http.StatusBadRequest, Code: "bad_request",
				Message: "reporter_email has no account, and guest submission is off, so the link to the ticket could not be used"}
		}
		in.GuestEmail = &addr
		in.GuestName = strings.TrimSpace(guestName)
		in.GuestPhone = strings.TrimSpace(guestPhone)
	default:
		return err
	}
	return nil
}

// fileOnBehalf is ResolveOnBehalf for the REST handler: it reports whether the
// request may go on, and when it returns false it has answered.
func (s *Server) fileOnBehalf(w http.ResponseWriter, r *http.Request, in *ticket.CreateInput,
	id *uuid.UUID, email *string, guestName, guestPhone string) bool {
	err := s.ResolveOnBehalf(r.Context(), authmw.GetActor(r).UserID, in, id, email, guestName, guestPhone)
	var refusal *OnBehalfError
	switch {
	case errors.As(err, &refusal) && refusal.Status == http.StatusTooManyRequests:
		tooManyAttempts(w, refusal.RetryAfter)
		return false
	case errors.As(err, &refusal):
		Error(w, refusal.Status, refusal.Code, refusal.Message)
		return false
	case err != nil:
		handleError(w, err)
		return false
	}
	return true
}

// GET /api/v1/tickets/{id}
func (s *Server) handleGetTicket(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	// Support both UUID and tracking number lookup.
	var t ticket.Ticket
	var err error
	var identifier string
	if uid, parseErr := uuid.Parse(id); parseErr == nil {
		identifier = uid.String()
		t, err = s.tickets.GetByID(r.Context(), uid)
	} else {
		identifier = strings.ToUpper(id)
		t, err = s.tickets.GetByTrackingNumber(r.Context(), ticket.TrackingNumber(identifier))
	}
	if err != nil {
		handleError(w, err)
		return
	}

	// One place decides who may see a ticket, for every role. Inlining the
	// rule per handler is what let the reporting-user check exist here while
	// staff scope existed nowhere.
	ok, err := s.canViewTicket(r, t)
	if err != nil {
		handleError(w, err)
		return
	}
	if !ok {
		ticketNotFound(w, identifier)
		return
	}

	views, err := s.ticketViews(r.Context(), []ticket.Ticket{t}, authmw.GetActor(r))
	if err != nil {
		handleError(w, err)
		return
	}
	// The record a restart replaced, for the page's "SLA restarted" badge: the
	// one-ticket read only, so a list never pays for it. Only a reopen restarts
	// a clock (#464), so it is absent on a ticket that was never reopened.
	if views[0].SLA != nil {
		earlier, err := s.slaPolicies.Earlier(r.Context(), t)
		if err != nil {
			handleError(w, err)
			return
		}
		views[0].SLA.Earlier = earlier
	}
	// The Requester line is staff's. A requester's response is filtered anyway
	// (it is not on their allowed list); not building it for them avoids the
	// lookup too.
	views[0].Requester = s.requesterFor(r.Context(), authmw.GetActor(r), t)
	// A closed ticket says whether it already has a follow-up (#625).
	if s.tickets.IsClosed(t) {
		if views[0].FollowUp, err = s.tickets.FollowUpOf(r.Context(), t.ID); err != nil {
			handleError(w, err)
			return
		}
	}
	JSON(w, http.StatusOK, viewerJSON(authmw.GetActor(r), views[0]))
}

// PATCH /api/v1/tickets/{id}
func (s *Server) handleUpdateTicket(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	id := ticketIDFrom(r)

	var body struct {
		StatusID        *uuid.UUID `json:"status_id"`
		AssigneeUserID  *uuid.UUID `json:"assignee_user_id"`
		AssigneeGroupID *uuid.UUID `json:"assignee_group_id"`
		// ClearAssignee unassigns the ticket. It is a separate flag because
		// both fields above decode into *uuid.UUID, where an explicit JSON
		// null and an omitted key both arrive as nil — so there is no way to
		// say "set this to nobody" with them alone. Same shape as
		// clear_category on SLA policies.
		ClearAssignee bool       `json:"clear_assignee"`
		CategoryID    *uuid.UUID `json:"category_id"`
		TypeID        *uuid.UUID `json:"type_id"`
		ItemID        *uuid.UUID `json:"item_id"`
		// Priority is staff and admin only, and re-matches the ticket's SLA
		// policy, keeping its clock (#443). A string, not ticket.Priority,
		// so a value that is not a priority reaches the service and answers
		// 400 there instead of being dropped.
		Priority *string `json:"priority"`
	}
	// No body at all is {}: nothing to change. Only here, not in DecodeJSON,
	// which every other route relies on to refuse an empty body.
	if err := DecodeJSON(r, &body); err != nil && !errors.Is(err, io.EOF) {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}

	if !s.requireUserIdentity(w, r) {
		return
	}
	actor := ticket.Actor{UserID: &a.UserID, Role: a.Role}

	// A value that is not a priority is a malformed request, answered before
	// anything else in it is saved. (ChangePriority checks it too, for every
	// other caller.)
	if body.Priority != nil && !ticket.Priority(*body.Priority).Valid() {
		Error(w, http.StatusBadRequest, "bad_request", "priority must be one of critical, high, medium, low")
		return
	}
	// Whether the caller may change a priority at all is a role question and
	// does not depend on the ticket, so it is answered here, before any other
	// change in the request is saved. (ChangePriority applies the same rule.)
	if body.Priority != nil {
		if err := ticket.CanChangePriority(actor.Role); err != nil {
			handleError(w, err)
			return
		}
	}

	// A refused classification must leave the whole request unapplied, so it is
	// checked before the status or the assignee is touched (#421). The write
	// itself stays where it was, after those.
	if body.CategoryID != nil {
		if a.Role == user.RoleUser {
			Error(w, http.StatusForbidden, "forbidden", "only staff can reclassify tickets")
			return
		}
		if err := s.tickets.ValidateCTI(r.Context(), id, *body.CategoryID, body.TypeID, body.ItemID); err != nil {
			handleError(w, err)
			return
		}
		// Classification only happens to an open ticket (409 ticket_closed),
		// asked here so a status or assignment in the same request is not
		// saved first. The service checks again on the locked row.
		if err := s.tickets.ValidateReclassify(r.Context(), id, body.StatusID); err != nil {
			handleError(w, err)
			return
		}
	}

	// A Resolved or Closed ticket's priority cannot change, and a request that
	// would leave the ticket in either state is refused whole: ChangePriority
	// runs last, so without this the status or assignment ahead of it would be
	// saved and then answered with a 409. After the 400 and the 403s above, so
	// those win when several things are wrong.
	if body.Priority != nil {
		if err := s.tickets.ValidatePriorityChange(r.Context(), id, body.StatusID); err != nil {
			handleError(w, err)
			return
		}
	}

	// Who the ticket may be given to is decided before the status is saved,
	// for the same reason (#468): UpdateStatus runs first, so a 400 or 403 from
	// the assignment after it left the status changed and an audit row
	// written. Assign checks the same things again on the locked row.
	//
	// The role check covers both branches below: clear_assignee is a separate
	// path from assignee_user_id, and guarding only one would leave a reporter
	// able to unassign the staff member working their ticket.
	if body.ClearAssignee || body.AssigneeUserID != nil || body.AssigneeGroupID != nil {
		if err := ticket.CanAssign(actor.Role); err != nil {
			handleError(w, err)
			return
		}
	}
	// The assignee is checked here, and not only by the column's foreign key,
	// because a foreign key can only answer 500.
	//
	// Four things got through: an unknown id (a typo, answered with "an
	// internal error occurred"), a deleted account (which puts the ticket
	// straight back into the limbo that deleting a user was just fixed to
	// avoid — it shows as unassigned and is missing from the unassigned
	// queue), a disabled account, and a reporting user, who is not
	// somebody work can be assigned to.
	if !body.ClearAssignee {
		if body.AssigneeUserID != nil {
			if err := s.assignableUser(r.Context(), *body.AssigneeUserID); err != nil {
				Error(w, http.StatusBadRequest, "bad_request", err.Error())
				return
			}
		}
		if body.AssigneeUserID != nil || body.AssigneeGroupID != nil {
			if err := s.tickets.ValidateAssignment(r.Context(), body.AssigneeUserID, body.AssigneeGroupID); err != nil {
				handleError(w, err)
				return
			}
		}
	}

	// The whole request is one change: one transaction, one event (#506).
	patch := ticket.Patch{StatusID: body.StatusID}
	if body.ClearAssignee {
		// Explicitly to nobody. Previously unreachable: the UI sent both
		// fields as undefined, which serialised to {} and skipped this branch
		// entirely, so "Clear assignment" returned 200 and changed nothing.
		patch.Assign = &ticket.Assignment{}
	} else if body.AssigneeUserID != nil || body.AssigneeGroupID != nil {
		patch.Assign = &ticket.Assignment{UserID: body.AssigneeUserID, GroupID: body.AssigneeGroupID}
	}
	if body.CategoryID != nil {
		if a.Role == user.RoleUser {
			Error(w, http.StatusForbidden, "forbidden", "only staff can reclassify tickets")
			return
		}
		patch.CTI = &ticket.Classification{CategoryID: *body.CategoryID, TypeID: body.TypeID, ItemID: body.ItemID}
	}
	if body.Priority != nil {
		p := ticket.Priority(*body.Priority)
		patch.Priority = &p
	}
	t, err := s.tickets.Update(r.Context(), id, patch, actor)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, viewerJSON(authmw.GetActor(r), t))
}

// POST /api/v1/tickets/{id}/replies
func (s *Server) handleAddReply(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	id := ticketIDFrom(r)

	var body struct {
		Body           string `json:"body"`
		Internal       bool   `json:"internal"`
		NotifyCustomer *bool  `json:"notify_customer"` // nil → defaults to true
		// Optional: plain (the default) or markdown. See restTextFormat.
		Format json.RawMessage `json:"format"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	format, err := restTextFormat(body.Format)
	if err != nil {
		handleError(w, err)
		return
	}
	if strings.TrimSpace(body.Body) == "" {
		Error(w, http.StatusBadRequest, "bad_request", "body is required")
		return
	}

	// A reply is written by somebody.
	if !s.requireUserIdentity(w, r) {
		return
	}

	// Internal replies are staff/admin only.
	if body.Internal && a.Role == user.RoleUser {
		Error(w, http.StatusForbidden, "forbidden", "only staff can post internal notes")
		return
	}

	// notify_customer defaults to true; forced false for internal notes.
	notifyCustomer := body.NotifyCustomer == nil || *body.NotifyCustomer
	if body.Internal {
		notifyCustomer = false
	}

	// Look up the reporter's email so the service can include it in the
	// notification event payload. A lookup failure is non-fatal — we skip
	// the email rather than rejecting the reply.
	var reporterEmail string
	if notifyCustomer {
		if t, err := s.tickets.GetByID(r.Context(), id); err == nil {
			reporterEmail = s.tickets.ReplyRecipient(r.Context(), t, ticket.Actor{UserID: &a.UserID, Role: a.Role})
		}
	}

	reopenDays := s.adminSvc.ReopenWindowDays(r.Context())
	reopenStatusID, err := s.reopenTargetStatusID(r.Context())
	if err != nil {
		handleError(w, err)
		return
	}
	if !s.requireUserIdentity(w, r) {
		return
	}
	actor := ticket.Actor{UserID: &a.UserID, Role: a.Role}
	reply, err := s.tickets.AddReply(r.Context(), id, body.Body, format, body.Internal, notifyCustomer, reporterEmail, actor, reopenDays, reopenStatusID)
	if err != nil {
		handleError(w, err)
		return
	}
	// The list names the author; so does the reply it was just created as.
	// A failed lookup leaves the name empty rather than failing a reply that
	// is already stored.
	if u, err := s.users.GetByID(r.Context(), a.UserID); err == nil {
		reply.AuthorName = u.DisplayName
	}
	JSON(w, http.StatusCreated, reply)
}

// GET /api/v1/tickets/{id}/replies
func (s *Server) handleListReplies(w http.ResponseWriter, r *http.Request) {
	id := ticketIDFrom(r)
	replies, err := s.tickets.ListReplies(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	// Access to the ticket is not access to the internal notes on it: the
	// reporter may read their own thread and must still not see staff-only
	// notes. This returned the raw rows.
	JSON(w, http.StatusOK, ticket.VisibleReplies(replies, authmw.GetActor(r).Role))
}

// POST /api/v1/tickets/{id}/resolve
func (s *Server) handleResolveTicket(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	id := ticketIDFrom(r)
	var body struct {
		Notes string `json:"notes"`
	}
	// An empty body is valid (resolve with no notes); anything else that does
	// not parse is the client's mistake, not a reason to drop its notes (#421).
	if err := DecodeJSON(r, &body); err != nil && !errors.Is(err, io.EOF) {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}

	if !s.requireUserIdentity(w, r) {
		return
	}
	actor := ticket.Actor{UserID: &a.UserID, Role: a.Role}
	t, err := s.tickets.Resolve(r.Context(), id, body.Notes, actor)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, viewerJSON(authmw.GetActor(r), t))
}

// POST /api/v1/tickets/{id}/reopen
//
// Force-reopens a Closed ticket, for the roles closed_reopen_policy allows
// (#349). Off by default, in which case every caller gets 409 ticket_closed
// with the reason; a requester gets 403 whatever the setting. The rule is
// ticket.Service.Reopen's, not decided here, so this route, a status change and
// Resolve cannot disagree about it.
func (s *Server) handleReopenTicket(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	if a.Role == user.RoleUser {
		Error(w, http.StatusForbidden, "forbidden", "users cannot directly reopen tickets")
		return
	}
	id := ticketIDFrom(r)
	targetID, err := s.reopenTargetStatusID(r.Context())
	if err != nil {
		handleError(w, err)
		return
	}
	if !s.requireUserIdentity(w, r) {
		return
	}
	actor := ticket.Actor{UserID: &a.UserID, Role: a.Role}
	t, err := s.tickets.Reopen(r.Context(), id, targetID, actor)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, viewerJSON(authmw.GetActor(r), t))
}

// POST /api/v1/tickets/{id}/follow-up
//
// The way forward from a Closed ticket (#349): a NEW ticket that starts open
// and links back to the closed one, which is left untouched. Closed is
// terminal by default, so this is the way forward in every mode; the reopen
// endpoint above is an alternative only where closed_reopen_policy allows it.
// Staff and admin only, and the rule lives in
// ticket.Service.CreateFollowUp (staff and admin) and CreateRequesterFollowUp
// (the ticket's own requester): ErrForbidden for anyone else, ErrNotClosed for
// a ticket that is not closed, ErrFollowUpExists for a requester's second.
func (s *Server) handleCreateFollowUp(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	id := ticketIDFrom(r)
	if !s.requireUserIdentity(w, r) {
		return
	}
	actor := ticket.Actor{UserID: &a.UserID, Role: a.Role}
	// A requester follows up their OWN closed ticket, holding to what they
	// could set on a normal create; staff and admin copy more. Which rule is the
	// service's (the role decides which method will accept the actor); the one
	// thing decided here is the active-classification check it cannot make.
	var (
		t   ticket.Ticket
		err error
	)
	// The follow-up copies the closed ticket's classification (a requester's
	// without the item, a guest's without the type), and is routed on that.
	orig, err := s.tickets.GetByID(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	if a.Role == user.RoleUser {
		t, err = s.tickets.CreateRequesterFollowUp(r.Context(), id, s.adminSvc.TicketPrefix(r.Context()), followUpChannel(a), actor,
			s.requesterClassificationOpen, s.Routing(r.Context(), orig.CategoryID, orig.TypeID)...)
	} else {
		t, err = s.tickets.CreateFollowUp(r.Context(), id, s.adminSvc.TicketPrefix(r.Context()), followUpChannel(a), actor,
			s.Routing(r.Context(), orig.CategoryID, orig.TypeID)...)
	}
	if err != nil {
		if errors.Is(err, ticket.ErrValidation) {
			Error(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		handleError(w, err)
		return
	}
	// Read back, so the response carries whoever the routing assigned.
	if fresh, err := s.tickets.GetByID(r.Context(), t.ID); err == nil {
		t = fresh
	}
	JSON(w, http.StatusCreated, viewerJSON(authmw.GetActor(r), t))
}

// POST /api/v1/tickets/{id}/close
func (s *Server) handleCloseTicket(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	if a.Role != user.RoleAdmin {
		Error(w, http.StatusForbidden, "forbidden", "only admins can close tickets")
		return
	}
	id := ticketIDFrom(r)
	// The admin who pressed the button, not SystemActor: the authorisation
	// check above is this handler's job, and the actor is for attribution.
	if !s.requireUserIdentity(w, r) {
		return
	}
	if err := s.tickets.Close(r.Context(), id, ticket.Actor{UserID: &a.UserID, Role: a.Role}); err != nil {
		handleError(w, err)
		return
	}
	t, err := s.tickets.GetByID(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, viewerJSON(authmw.GetActor(r), t))
}

// POST /api/v1/tickets/{id}/links
func (s *Server) handleAddLink(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	sourceID := ticketIDFrom(r)
	var body struct {
		TargetID           uuid.UUID `json:"target_id"`
		LinkType           string    `json:"link_type"`
		ResolveAsDuplicate bool      `json:"resolve_as_duplicate"`
		ResolutionNotes    string    `json:"resolution_notes"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	// The path gate authorised {id}; this request names a second ticket. Both
	// ends need the check, because the link is written onto the TARGET's
	// thread too: a reporting user could otherwise mark a ticket it cannot
	// read as a duplicate of its own, and tell existing from nonexistent
	// target ids by the difference between 204 and a foreign-key failure.
	ok, err := s.canViewTicketID(r, body.TargetID)
	if err != nil {
		handleError(w, err)
		return
	}
	if !ok {
		ticketNotFound(w, body.TargetID.String())
		return
	}

	if !s.requireUserIdentity(w, r) {
		return
	}
	actor := ticket.Actor{UserID: &a.UserID, Role: a.Role}

	// If resolve_as_duplicate is true, validate that link_type is actually duplicate_of
	if body.ResolveAsDuplicate && ticket.LinkType(body.LinkType) != ticket.LinkDuplicateOf {
		Error(w, http.StatusBadRequest, "bad_request", "resolve_as_duplicate can only be used with duplicate_of link type")
		return
	}

	if body.ResolveAsDuplicate {
		// Resolve the source ticket as a duplicate of the target in one transaction.
		t, err := s.tickets.ResolveAsDuplicate(r.Context(), sourceID, body.TargetID, body.ResolutionNotes, actor)
		if err != nil {
			handleError(w, err)
			return
		}
		JSON(w, http.StatusOK, viewerJSON(authmw.GetActor(r), t))
		return
	}

	// Original path: just create the link.
	if err := s.tickets.AddLink(r.Context(), sourceID, body.TargetID, ticket.LinkType(body.LinkType), actor); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DELETE /api/v1/tickets/{id}/links/{targetId}/{linkType}
func (s *Server) handleRemoveLink(w http.ResponseWriter, r *http.Request) {
	sourceID := ticketIDFrom(r)
	targetID, err := uuid.Parse(chi.URLParam(r, "targetId"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid target ID")
		return
	}
	// The same check the other end of this pair makes, and for the same
	// reason: the link lives on the TARGET's thread too, so removing one
	// touches a ticket this request has not been authorised for. Without it a
	// reporting user could delete a link staff had put on their ticket —
	// "duplicate of GHD-2026-000123" is a staff judgement about the queue,
	// not something the reporter gets to overrule — and could tell a real
	// target id from an invented one by the difference between 204 and a
	// failure. See #211.
	ok, err := s.canViewTicketID(r, targetID)
	if err != nil {
		handleError(w, err)
		return
	}
	if !ok {
		ticketNotFound(w, targetID.String())
		return
	}

	// And it is a staff decision to make. handleAddLink is reachable by a
	// reporter only for two tickets they own; removal had no role check at
	// all, so staff classification on a reporter's own ticket was theirs to
	// undo.
	a := authmw.GetActor(r)
	if a == nil || (a.Role != user.RoleAdmin && a.Role != user.RoleStaff) {
		Error(w, http.StatusForbidden, "forbidden", "only staff can remove a ticket link")
		return
	}

	lt := ticket.LinkType(chi.URLParam(r, "linkType"))
	// An unrecognized link type would otherwise delete nothing and still
	// answer 204 — not incorrect (there is indeed no such link), but silently
	// misleading about why nothing happened. See #201.
	if !lt.Valid() {
		Error(w, http.StatusBadRequest, "bad_request", "invalid link type")
		return
	}
	// Unlike adding a link, removing one has never required a credential with a
	// person behind it, so that stays: such a caller is announced with no actor.
	actor := ticket.Actor{Role: a.Role}
	if a.UserID != uuid.Nil {
		actor.UserID = &a.UserID
	}
	if err := s.tickets.RemoveLink(r.Context(), sourceID, targetID, lt, actor); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/v1/tickets/{id}/history
func (s *Server) handleListStatusHistory(w http.ResponseWriter, r *http.Request) {
	id := ticketIDFrom(r)
	// Status history is ticket content: gate it on the same rule as the ticket.
	{
		t, err := s.tickets.GetByID(r.Context(), id)
		if err != nil {
			handleError(w, err)
			return
		}
		ok, err := s.canViewTicket(r, t)
		if err != nil {
			handleError(w, err)
			return
		}
		if !ok {
			ticketNotFound(w, id.String())
			return
		}
	}
	history, err := s.tickets.ListStatusHistory(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, s.maskStatusHistory(r.Context(), history))
}

// statusHistoryEntryView is a status timeline row as sent: the entry, with
// changed_by_masked when its name was withheld, so the label "Requester" is not
// mistaken for an account called that (the same flag as actor_masked).
type statusHistoryEntryView struct {
	ticket.StatusHistoryEntry
	ChangedByMasked bool `json:"changed_by_masked,omitempty"`
}

// maskStatusHistory applies the requester-name mask to the status timeline, by
// the rule the Activity feed beside it uses (auditActorName, the ticket log
// view): who changed a status is audit output, so it must not name a requester
// where the feed would not. changed_by_user_id stays, as actor_id does. One
// lookup for all the people on the timeline.
func (s *Server) maskStatusHistory(ctx context.Context, history []ticket.StatusHistoryEntry) []statusHistoryEntryView {
	out := make([]statusHistoryEntryView, len(history))
	for i, h := range history {
		out[i] = statusHistoryEntryView{StatusHistoryEntry: h}
	}
	// The lookup runs under every value of the setting: "a deleted account" is
	// not the requester mask's to decide, so it is withheld even where the
	// ticket log does not mask requesters (admin_log). Only the "Requester"
	// label is behind the setting.
	maskRequesters := s.masksRequestersIn(ctx, admin.MaskRequesterNamesTicketLog)
	seen := map[uuid.UUID]bool{}
	var ids []uuid.UUID
	for _, h := range history {
		if h.ChangedByUserID != nil && !seen[*h.ChangedByUserID] {
			seen[*h.ChangedByUserID] = true
			ids = append(ids, *h.ChangedByUserID)
		}
	}
	if len(ids) == 0 {
		return out
	}
	// Fails closed: a name stays only for an account positively found and known
	// to be staff or an administrator. The name on the row comes from a join, not
	// from this lookup, so an account the lookup does not return still has its
	// name in the row, and must not be shown. One that is not returned is a
	// deleted account, of any role, and is called that (owner, 2026-11-11); one
	// whose role is a requester's is masked, where the setting masks. When the
	// lookup itself fails nothing is known, so every name is withheld as the
	// requester mask, whatever the setting.
	roles := map[uuid.UUID]user.Role{}
	users, err := s.users.GetByIDs(ctx, ids)
	if err != nil {
		slog.ErrorContext(ctx, "resolving status history roles failed", "error", err)
	}
	for _, u := range users {
		roles[u.ID] = u.Role
	}
	for i := range out {
		id := out[i].ChangedByUserID
		if id == nil {
			continue
		}
		role, found := roles[*id]
		switch {
		case err != nil:
			out[i].ChangedByName, out[i].ChangedByMasked = "Requester", true
		case !found:
			out[i].ChangedByName = deletedAccountName
		case maskRequesters && role == user.RoleUser:
			out[i].ChangedByName, out[i].ChangedByMasked = "Requester", true
		}
	}
	return out
}

// ticketAuditEntryView is what handleListTicketAudit returns: enough to
// answer "who did what, and when" for every viewer, plus the field-level
// before/after diff for the viewers #129's own settled-later question
// allows it for. Before/After are omitted (not merely null) for anyone who
// does not clear that gate — see the role switch in handleListTicketAudit.
type ticketAuditEntryView struct {
	ID          uuid.UUID      `json:"id"`
	Action      string         `json:"action"`
	ActorID     *uuid.UUID     `json:"actor_id"`
	ActorName   string         `json:"actor_name,omitempty"`
	ActorMasked bool           `json:"actor_masked,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	Before      map[string]any `json:"before,omitempty"`
	After       map[string]any `json:"after,omitempty"`
	// Names: see adminAuditEntryView.Names.
	Names map[string]auditName `json:"names,omitempty"`
}

// GET /api/v1/tickets/{id}/audit
//
// #129: a per-ticket activity feed next to the status timeline. Staff and
// admin only (the route sits behind RequireRole in routes.go, after the
// ticket-access check, so a ticket out of reach is a 404 first): since
// 2026-10-15 a requester sees the ticket's current state and its
// conversation, not its history or audit trail, and is refused with 403.
//
// The RoleUser branch below, which withholds the actor of assigned/unassigned
// entries, is therefore unreachable today. It is kept as a second safeguard
// (assignment is staff/admin-only, see ticket.CanAssign, and nothing else on a
// ticket discloses that actor's identity to a reporting user), so that loosening
// the route gate by mistake does not start naming staff to requesters; the
// same way VisibleReplies withholds internal-note authorship.
//
// The field-level before/after diff is a second, independent gate, settled
// by the admin-wide view's own design round: admin always sees it; staff
// sees it only when admin.KeyStaffCanViewTicketChangeHistory is on (off by
// default — #325 shipped without it, and turning on a wider disclosure
// silently during an upgrade is not this endpoint's call to make); a
// reporting user never sees it, full stop, independent of the setting. What
// is shown is redacted the same way the admin-wide view redacts it — see
// audit.Redact — because a sensitive field name would be exactly as
// sensitive here as there.
func (s *Server) handleListTicketAudit(w http.ResponseWriter, r *http.Request) {
	id := ticketIDFrom(r)
	role := authmw.GetActor(r).Role
	showDiff := role == user.RoleAdmin ||
		(role == user.RoleStaff && s.adminSvc.StaffCanViewTicketChangeHistory(r.Context()))

	// Follow and unfollow entries (#441): administrators always, staff only
	// when the administrator allows it, a requester never. Left out by the
	// query rather than dropped afterwards, so they never count toward the
	// feed's cap and cannot push the ticket's real history off it.
	showFollow := role == user.RoleAdmin ||
		(role == user.RoleStaff && s.adminSvc.StaffCanViewFollowAudit(r.Context()))
	entries, err := s.tickets.ListAuditFeed(r.Context(), id, showFollow)
	if err != nil {
		handleError(w, err)
		return
	}

	views := make([]ticketAuditEntryView, len(entries))
	// Cached per request: the same actor (an assignee working a long
	// thread, say) can appear on many entries, and a lookup miss (the
	// account was later deleted) is cached too, so it costs one failed
	// GetByID rather than one per entry.
	maskRequesters := s.masksRequestersIn(r.Context(), admin.MaskRequesterNamesTicketLog)
	names := make(map[uuid.UUID]auditActor)
	diffs := make([]map[string]any, 0, 2*len(entries))
	for i, e := range entries {
		v := ticketAuditEntryView{ID: e.ID, Action: e.Action, ActorID: e.ActorID, CreatedAt: e.CreatedAt}
		// A viewer who may see follow entries always gets their before and
		// after, redacted like any diff: an unfollowed entry written when a
		// ticket was reassigned has the reassigning actor as its actor, and
		// without the diff it would read as that person stopping, not as
		// whose follow it was.
		if showDiff || (showFollow && ticket.IsFollowAuditAction(e.Action)) {
			v.Before, v.After = audit.Redact(e.Before, e.After)
			diffs = append(diffs, v.Before, v.After)
		}
		if role == user.RoleUser && (e.Action == "assigned" || e.Action == "unassigned") {
			v.ActorID = nil
			views[i] = v
			continue
		}
		if e.ActorID != nil {
			a, cached := names[*e.ActorID]
			if !cached {
				u, err := s.users.GetByID(r.Context(), *e.ActorID)
				switch {
				case err == nil:
					a = auditActorName(u, maskRequesters)
				case errors.Is(err, user.ErrNotFound):
					// Account since deleted; actor_id stays (it is a fact
					// about what happened). The view says "Deleted account",
					// of any role, and never the name (owner, 2026-11-11).
					a = auditActor{name: deletedAccountName}
				default:
					slog.ErrorContext(r.Context(), "resolving audit actor name failed", "actor_id", *e.ActorID, "error", err)
				}
				names[*e.ActorID] = a
			}
			v.ActorName = a.name
			v.ActorMasked = a.masked
		}
		views[i] = v
	}
	// See handleListAdminAudit: resolved from the diffs as sent, in one batch.
	if len(diffs) > 0 {
		resolved := s.resolveAuditNames(r.Context(), maskRequesters, diffs...)
		for i := range views {
			views[i].Names = resolved.forEntry(views[i].Before, views[i].After)
		}
	}
	JSON(w, http.StatusOK, views)
}

// GET /api/v1/tickets/{id}/links
func (s *Server) handleListLinks(w http.ResponseWriter, r *http.Request) {
	id := ticketIDFrom(r)
	links, err := s.tickets.ListLinks(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, links)
}

// Routing works out who a newly created ticket may be routed to, best first:
// the CTI-scoped group, else the global group, else the round-robin users, else
// nobody. Every door that creates a ticket calls it (REST create, the follow-ups,
// the guest form, and the MCP tools through mcp.Router), because a follow-up is a
// new ticket and goes through the same routing (#349). The ticket service assigns
// the first one the assignment accepts, inside the create's transaction (#506).
func (s *Server) Routing(ctx context.Context, categoryID uuid.UUID, typeID *uuid.UUID) []ticket.Assignment {
	if matched, _ := s.groups.GetGroupsForTicket(ctx, categoryID, typeID); len(matched) > 0 {
		gid := matched[0].ID
		return []ticket.Assignment{{GroupID: &gid}}
	}
	if gid := s.adminSvc.AutoAssignGroupID(ctx); gid != nil {
		return []ticket.Assignment{{GroupID: gid}}
	}
	uids := s.adminSvc.AutoAssignUserIDs(ctx)
	if len(uids) == 0 {
		return nil
	}
	return s.roundRobin(uids)
}

// roundRobin offers every user, rotated from the next slot. The turn is taken
// with one atomic add, so creates that overlap each get their own.
//
// Nothing removes a departed colleague from this setting, so the list
// outlives them. Assign refuses a deleted, disabled or non-staff account —
// but taking their slot and giving up would silently leave every Nth ticket
// unassigned, which is a queue that quietly loses a share of its work. The
// whole list is offered, rotated, so the next one is tried instead.
func (s *Server) roundRobin(uids []uuid.UUID) []ticket.Assignment {
	start := s.rrIdx.Add(1)
	out := make([]ticket.Assignment, len(uids))
	for i := range uids {
		uid := uids[(start+uint64(i))%uint64(len(uids))]
		out[i] = ticket.Assignment{UserID: &uid}
	}
	return out
}

// reopenTargetStatusID resolves the configured reopen target, falling back to
// the New system status when it does not resolve.
//
// It can fail to resolve: the setting takes any string, and a custom status can
// be deleted while still configured here. Passing uuid.Nil through hit the
// status_id foreign key mid-transaction and took the user's reply with it, so a
// customer replying to their own resolved ticket got a 500 because an
// administrator had mistyped a setting. The fallback keeps the customer
// working; the misconfiguration is an admin problem and is logged.
//
// Shared by the authenticated reply path and the guest reply path: both reopen
// a Resolved ticket inside the window, and need to land on the same status a
// misconfigured or absent setting should fall back to, and the force-reopen
// endpoint (#349), which lands there too.
func (s *Server) reopenTargetStatusID(ctx context.Context) (uuid.UUID, error) {
	statuses, err := s.tickets.ListStatuses(ctx)
	if err != nil {
		// Returned rather than swallowed. Extracting this from handleAddReply
		// briefly turned a database failure into uuid.Nil, which the domain
		// reads as a misconfigured setting and refuses with 400 "no valid
		// reopen target status is configured" — an outage reported as an
		// administrator's typo.
		return uuid.Nil, err
	}
	want := s.adminSvc.ReopenTargetStatusName(ctx)
	inactive := false
	for _, st := range statuses {
		if st.Name != want {
			continue
		}
		// A deactivated status is retired for everyone, so nothing is reopened
		// into it. The target is chosen by name from active statuses and can
		// be deactivated afterwards; reopening then goes to New, like an
		// unresolvable name, until an administrator picks another. The admin
		// security warnings say so.
		if !st.Active {
			inactive = true
			break
		}
		return st.ID, nil
	}
	for _, st := range statuses {
		if st.Name == ticket.StatusNameNew {
			// One message, for the one condition that applies.
			if inactive {
				slog.WarnContext(ctx, "configured reopen target status is inactive; falling back to New",
					"configured", want)
			} else {
				slog.WarnContext(ctx, "configured reopen target status does not exist; falling back to New",
					"configured", want)
			}
			return st.ID, nil
		}
	}
	return uuid.Nil, nil
}

// categoryIsOpen reports whether a category is one a reporter may file into.
//
// Active categories only. An archived category is one an administrator has
// deliberately taken out of circulation, and the list a reporter is offered
// does not contain it — so filing into one means the id came from somewhere
// else, which is not a thing to accept silently.
//
// Staff are held to the same rule by the ticket service (refuseArchivedPick,
// #421); this is the reporter's check, made ahead of it for the reporter's
// own wording and so the guest form can use it before anything is written.
func (s *Server) categoryIsOpen(ctx context.Context, id uuid.UUID) error {
	cats, err := s.categories.ListCategories(ctx, true)
	if err != nil {
		return err
	}
	for _, c := range cats {
		if c.ID == id {
			return nil
		}
	}
	return errors.New("category_id is not an active category")
}

// requesterClassificationOpen is the creation rule a requester is held to for a
// ticket's classification, as handleCreateTicket applies it: the category must
// be active, and the type, when there is one, active too. A requester's
// follow-up is a new ticket and is held to it (#349); a staff follow-up copies
// the closed ticket's classification and is not.
func (s *Server) requesterClassificationOpen(ctx context.Context, categoryID uuid.UUID, typeID *uuid.UUID) error {
	if err := s.categoryIsOpen(ctx, categoryID); err != nil {
		return err
	}
	if typeID != nil {
		ty, err := s.categories.GetType(ctx, *typeID)
		if err != nil || ty.CategoryID != categoryID {
			return errors.New("type_id does not belong to category_id")
		}
		if !ty.Active {
			return errors.New("type_id is not an active type")
		}
	}
	return nil
}

// requireUserIdentity refuses a credential that has nobody behind it, and
// reports whether the caller may go on.
//
// An OAuth client actor carries no user id. Everything below records who
// acted — an audit entry, a status-history row, a reply's author, a ticket's
// reporter — and all of those are foreign keys to users, so uuid.Nil reached
// the insert and came back 500 with a constraint name in the log. Some of
// those writes had already taken a tracking number from the sequence.
//
// The scope catalogue advertises tickets:write to OAuth clients, so this is a
// capability the API offers and cannot deliver. Saying so is the honest
// answer: the request is not malformed and the server is not broken, the
// credential simply is not a person.
func (s *Server) requireUserIdentity(w http.ResponseWriter, r *http.Request) bool {
	a := authmw.GetActor(r)
	if a == nil || a.UserID == uuid.Nil {
		Error(w, http.StatusForbidden, "user_identity_required",
			"this credential has no user identity, so it cannot be recorded as having acted on a ticket")
		return false
	}
	return true
}

// assignableUser reports why a user cannot be given a ticket, or nil.
//
// Work goes to somebody who can do it: an account that exists, is not
// deleted, is not disabled, and belongs to staff. A reporting user is not a
// queue.
func (s *Server) assignableUser(ctx context.Context, id uuid.UUID) error {
	u, err := s.users.GetByID(ctx, id)
	if err != nil {
		return errors.New("assignee_user_id is not a user on this help desk")
	}
	if u.DeletedAt != nil {
		return errors.New("that account has been deleted")
	}
	if u.Disabled {
		return errors.New("that account is disabled")
	}
	if u.Role == user.RoleUser {
		return errors.New("tickets are assigned to staff, and that account is a reporting user")
	}
	return nil
}
