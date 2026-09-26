package server

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// GET /api/v1/tickets
// Returns tickets relevant to the current user:
//   - admin/staff: tickets assigned to them + tickets assigned to any of their groups
//   - user: tickets they reported
//
// Optional query params:
//   - assignee_group_id=<uuid> — tickets for a specific group (staff/admin only).
//   - scope=mine|unassigned|all — admin-only scopes. "unassigned" returns tickets
//     with no assignee user or group. "all" returns every ticket. Defaults to "mine".
//
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
		// A negative offset is refused for the same reason it used to 500.
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			offset = min(n, math.MaxInt32)
		}
	}
	return limit, offset
}

func (s *Server) handleListTickets(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	limit, offset := pageParams(r)

	// Admin-only: filter all tickets by reporter user ID.
	if ridStr := r.URL.Query().Get("reporter_id"); ridStr != "" {
		if a.Role != user.RoleAdmin {
			Error(w, http.StatusForbidden, "forbidden", "only admins can filter by reporter")
			return
		}
		rid, err := uuid.Parse(ridStr)
		if err != nil {
			Error(w, http.StatusBadRequest, "bad_request", "invalid reporter_id")
			return
		}
		var tickets []ticket.Ticket
		if q != "" {
			tickets, err = s.tickets.SearchByReporter(ctx, rid, q, limit, offset)
		} else {
			tickets, err = s.tickets.ListByReporter(ctx, rid, limit, offset)
		}
		if err != nil {
			handleError(w, err)
			return
		}
		s.writeTickets(w, r, tickets)
		return
	}

	// Specific group filter (staff/admin only).
	if gidStr := r.URL.Query().Get("assignee_group_id"); gidStr != "" {
		if a.Role == user.RoleUser {
			Error(w, http.StatusForbidden, "forbidden", "users cannot list group tickets")
			return
		}
		gid, err := uuid.Parse(gidStr)
		if err != nil {
			Error(w, http.StatusBadRequest, "bad_request", "invalid assignee_group_id")
			return
		}
		// Refusing only RoleUser was not enough. With scope enforced, the
		// default listing and GET /tickets/{id} both refuse a ticket outside
		// the caller's groups — and this branch handed the same tickets over to
		// anyone who named the group. Group ids are listed to every staff
		// member by GET /groups, so it was a filter, not a boundary. An OAuth
		// client acts as staff and belongs to no group at all, which made it
		// every ticket assigned to any group.
		if a.Role == user.RoleStaff && s.adminSvc.TicketScopeEnforced(ctx) {
			scope, err := s.staffScopeFor(ctx, a)
			if err != nil {
				handleError(w, err)
				return
			}
			if !slices.Contains(scope.GroupIDs, gid) {
				Error(w, http.StatusForbidden, "forbidden",
					"you are not a member of that group")
				return
			}
		}
		var tickets []ticket.Ticket
		if q != "" {
			tickets, err = s.tickets.SearchByAssigneeGroup(ctx, gid, q, limit, offset)
		} else {
			tickets, err = s.tickets.ListByAssigneeGroup(ctx, gid, limit, offset)
		}
		if err != nil {
			handleError(w, err)
			return
		}
		s.writeTickets(w, r, tickets)
		return
	}

	// Admin-only scopes: unassigned queue or everything.
	if scope == "unassigned" || scope == "all" {
		if a.Role != user.RoleAdmin {
			Error(w, http.StatusForbidden, "forbidden", "only admins can use this scope")
			return
		}
		var (
			tickets []ticket.Ticket
			err     error
		)
		switch scope {
		case "unassigned":
			if q != "" {
				tickets, err = s.tickets.SearchUnassigned(ctx, q, limit, offset)
			} else {
				tickets, err = s.tickets.ListUnassigned(ctx, limit, offset)
			}
		case "all":
			if q != "" {
				tickets, err = s.tickets.SearchAll(ctx, q, limit, offset)
			} else {
				tickets, err = s.tickets.ListAll(ctx, limit, offset)
			}
		}
		if err != nil {
			handleError(w, err)
			return
		}
		s.writeTickets(w, r, tickets)
		return
	}

	// Users only see their own reported tickets.
	if a.Role == user.RoleUser {
		var (
			tickets []ticket.Ticket
			err     error
		)
		if q != "" {
			tickets, err = s.tickets.SearchByReporter(ctx, a.UserID, q, limit, offset)
		} else {
			tickets, err = s.tickets.ListByReporter(ctx, a.UserID, limit, offset)
		}
		if err != nil {
			handleError(w, err)
			return
		}
		s.writeTickets(w, r, tickets)
		return
	}

	// With scope enforcement on, a staff member's default list is everything
	// the scope model admits — including unassigned tickets in a category
	// their groups cover. Without that the queue is invisible: nothing is
	// assigned yet, so nobody can pick it up.
	//
	// Admins keep the existing path; they see everything either way.
	if a.Role == user.RoleStaff && s.adminSvc.TicketScopeEnforced(ctx) {
		var (
			tickets []ticket.Ticket
			err     error
		)
		if q != "" {
			tickets, err = s.tickets.SearchVisibleToStaff(ctx, a.UserID, q, limit, offset)
		} else {
			tickets, err = s.tickets.ListVisibleToStaff(ctx, a.UserID, limit, offset)
		}
		if err != nil {
			handleError(w, err)
			return
		}
		s.writeTickets(w, r, tickets)
		return
	}

	// Staff/admin (scope=mine): tickets assigned to them + tickets assigned to
	// their groups.
	//
	// This branch merges 1+N queries, so the page window cannot be pushed down
	// into each of them — applying limit and offset per query and concatenating
	// returned limit×(1+groups) rows and skipped offset rows in every sublist
	// independently. Each source is therefore read up to the end of the
	// requested page, merged, and sliced once.
	//
	// The cost is bounded by the offset ceiling, and the indexes added in
	// migration 21 cover the ordering.
	window := offset + limit
	all := make([]ticket.Ticket, 0, window)

	var err error
	var mine []ticket.Ticket
	if q != "" {
		mine, err = s.tickets.SearchByAssigneeUser(ctx, a.UserID, q, window, 0)
	} else {
		mine, err = s.tickets.ListByAssigneeUser(ctx, a.UserID, window, 0)
	}
	if err != nil {
		handleError(w, err)
		return
	}
	all = append(all, mine...)

	groups, err := s.groups.ListGroupsForUser(ctx, a.UserID)
	if err != nil {
		handleError(w, err)
		return
	}
	seen := make(map[uuid.UUID]bool, len(mine))
	for _, t := range mine {
		seen[t.ID] = true
	}
	for _, g := range groups {
		var gTickets []ticket.Ticket
		if q != "" {
			gTickets, err = s.tickets.SearchByAssigneeGroup(ctx, g.ID, q, window, 0)
		} else {
			gTickets, err = s.tickets.ListByAssigneeGroup(ctx, g.ID, window, 0)
		}
		if err != nil {
			handleError(w, err)
			return
		}
		for _, t := range gTickets {
			if !seen[t.ID] {
				seen[t.ID] = true
				all = append(all, t)
			}
		}
	}

	// Sorted here because the merge destroyed the per-query ordering, with the
	// id as a tiebreaker: created_at alone is not unique, and without a
	// tiebreaker two tickets sharing a timestamp can swap places between pages
	// — showing one twice and hiding the other.
	slices.SortFunc(all, func(x, y ticket.Ticket) int {
		if c := y.CreatedAt.Compare(x.CreatedAt); c != 0 {
			return c // newest first
		}
		return strings.Compare(x.ID.String(), y.ID.String())
	})

	if offset >= len(all) {
		s.writeTickets(w, r, nil)
		return
	}
	s.writeTickets(w, r, all[offset:min(offset+limit, len(all))])
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
		Subject     string     `json:"subject"`
		Description string     `json:"description"`
		CategoryID  uuid.UUID  `json:"category_id"`
		TypeID      *uuid.UUID `json:"type_id"`
		ItemID      *uuid.UUID `json:"item_id"`
		Priority    string     `json:"priority"`
		// Guest-only fields
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
		// Staff and administrators are not held to this, for the same reason
		// they are not held to it on the category: filing or reclassifying an
		// old ticket under the classification it actually belongs to is
		// ordinary work, and that classification may well be archived.
		if !isStaffOrAdmin && !ty.Active {
			Error(w, http.StatusBadRequest, "bad_request",
				"type_id is not an active type")
			return
		}
	}

	in := ticket.CreateInput{
		Subject:     body.Subject,
		Description: body.Description,
		CategoryID:  body.CategoryID,
		TypeID:      body.TypeID,
		ItemID:      body.ItemID,
		Priority:    ticket.Priority(body.Priority),
	}
	if in.Priority == "" {
		in.Priority = ticket.PriorityMedium
	}

	if !isGuest {
		// A credential with no person behind it cannot be a reporter.
		if !s.requireUserIdentity(w, r) {
			return
		}
		in.ReporterUserID = &a.UserID
	} else {
		email := body.GuestEmail
		in.GuestEmail = &email
		in.GuestName = strings.TrimSpace(body.GuestName)
		in.GuestPhone = strings.TrimSpace(body.GuestPhone)
	}

	in.TrackingPrefix = s.adminSvc.TicketPrefix(r.Context())

	t, err := s.tickets.Create(r.Context(), in)
	if err != nil {
		if errors.Is(err, ticket.ErrValidation) {
			Error(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		handleError(w, err)
		return
	}

	// Auto-assign: CTI-scoped group first, then global group, then round-robin users, else unassigned.
	if matched, _ := s.groups.GetGroupsForTicket(r.Context(), t.CategoryID, t.TypeID); len(matched) > 0 {
		gid := matched[0].ID
		_, _ = s.tickets.Assign(r.Context(), t.ID, nil, &gid, ticket.SystemActor)
	} else if gid := s.adminSvc.AutoAssignGroupID(r.Context()); gid != nil {
		_, _ = s.tickets.Assign(r.Context(), t.ID, nil, gid, ticket.SystemActor)
	} else if uids := s.adminSvc.AutoAssignUserIDs(r.Context()); len(uids) > 0 {
		// Round-robin, skipping anybody the assignment refuses.
		//
		// Nothing removes a departed colleague from this setting, so the list
		// outlives them. Assign now refuses a deleted, disabled or
		// non-staff account — but taking their slot and giving up would
		// silently leave every Nth ticket unassigned, which is a queue that
		// quietly loses a share of its work. Try the next one instead.
		start := s.rrIdx.Add(1)
		for i := range uids {
			uid := uids[(start+uint64(i))%uint64(len(uids))]
			if _, err := s.tickets.Assign(r.Context(), t.ID, &uid, nil, ticket.SystemActor); err == nil {
				break
			}
		}
	}

	// Set any custom field values supplied on creation.
	//
	// Best-effort — the ticket exists and is not going to be refused over a
	// field — but only for fields this ticket actually has. Without the
	// check, any field id supplied on creation was stored, including one
	// assigned to no scope and therefore offered to nobody.
	if len(body.CustomFields) > 0 {
		allowed, err := s.customFields.ResolveFieldsForCTI(r.Context(), t.CategoryID, t.TypeID, t.ItemID)
		if err == nil {
			onTicket := make(map[uuid.UUID]bool, len(allowed))
			for _, a := range allowed {
				onTicket[a.FieldDefID] = true
			}
			for fieldDefIDStr, value := range body.CustomFields {
				if value == "" {
					continue
				}
				fieldDefID, parseErr := uuid.Parse(fieldDefIDStr)
				if parseErr != nil || !onTicket[fieldDefID] {
					continue
				}
				_ = s.customFields.SetValue(r.Context(), t.ID, fieldDefID, value)
			}
		}
	}

	JSON(w, http.StatusCreated, t)
}

// GET /api/v1/tickets/{id}
func (s *Server) handleGetTicket(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	// Support both UUID and tracking number lookup.
	var t ticket.Ticket
	var err error
	if uid, parseErr := uuid.Parse(id); parseErr == nil {
		t, err = s.tickets.GetByID(r.Context(), uid)
	} else {
		t, err = s.tickets.GetByTrackingNumber(r.Context(), ticket.TrackingNumber(strings.ToUpper(id)))
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
		Error(w, http.StatusForbidden, "forbidden", "not your ticket")
		return
	}

	views, err := s.ticketViews(r.Context(), []ticket.Ticket{t}, authmw.GetActor(r))
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, views[0])
}

// PATCH /api/v1/tickets/{id}
func (s *Server) handleUpdateTicket(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ticket ID")
		return
	}

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
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}

	if !s.requireUserIdentity(w, r) {
		return
	}
	actor := ticket.Actor{UserID: &a.UserID, Role: a.Role}

	if body.StatusID != nil {
		if _, err := s.tickets.UpdateStatus(r.Context(), id, *body.StatusID, actor); err != nil {
			handleError(w, err)
			return
		}
	}
	// Covers both branches below: clear_assignee is a separate path from
	// assignee_user_id, and guarding only one would leave a reporter able to
	// unassign the staff member working their ticket.
	if body.ClearAssignee || body.AssigneeUserID != nil || body.AssigneeGroupID != nil {
		if err := ticket.CanAssign(actor.Role); err != nil {
			handleError(w, err)
			return
		}
	}

	if body.ClearAssignee {
		// Explicitly to nobody. Previously unreachable: the UI sent both
		// fields as undefined, which serialised to {} and skipped this branch
		// entirely, so "Clear assignment" returned 200 and changed nothing.
		if _, err := s.tickets.Assign(r.Context(), id, nil, nil, actor); err != nil {
			handleError(w, err)
			return
		}
	} else if body.AssigneeUserID != nil || body.AssigneeGroupID != nil {
		// Checked before the write, because the column's foreign key is the
		// only thing that was checking it — and a foreign key can only
		// answer 500.
		//
		// Four things got through: an unknown id (a typo, answered with "an
		// internal error occurred"), a deleted account (which puts the ticket
		// straight back into the limbo that deleting a user was just fixed to
		// avoid — it shows as unassigned and is missing from the unassigned
		// queue), a disabled account, and a reporting user, who is not
		// somebody work can be assigned to.
		if body.AssigneeUserID != nil {
			if err := s.assignableUser(r.Context(), *body.AssigneeUserID); err != nil {
				Error(w, http.StatusBadRequest, "bad_request", err.Error())
				return
			}
		}
		if _, err := s.tickets.Assign(r.Context(), id, body.AssigneeUserID, body.AssigneeGroupID, actor); err != nil {
			handleError(w, err)
			return
		}
	}
	if body.CategoryID != nil {
		if a.Role == user.RoleUser {
			Error(w, http.StatusForbidden, "forbidden", "only staff can reclassify tickets")
			return
		}
		if _, err := s.tickets.UpdateCTI(r.Context(), id, *body.CategoryID, body.TypeID, body.ItemID); err != nil {
			handleError(w, err)
			return
		}
	}

	t, err := s.tickets.GetByID(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, t)
}

// POST /api/v1/tickets/{id}/replies
func (s *Server) handleAddReply(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ticket ID")
		return
	}

	var body struct {
		Body           string `json:"body"`
		Internal       bool   `json:"internal"`
		NotifyCustomer *bool  `json:"notify_customer"` // nil → defaults to true
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
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
			if t.GuestEmail != nil {
				reporterEmail = *t.GuestEmail
			} else if t.ReporterUserID != nil {
				if u, err := s.users.GetByID(r.Context(), *t.ReporterUserID); err == nil {
					reporterEmail = u.Email
				}
			}
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
	reply, err := s.tickets.AddReply(r.Context(), id, body.Body, body.Internal, notifyCustomer, reporterEmail, actor, reopenDays, reopenStatusID)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusCreated, reply)
}

// GET /api/v1/tickets/{id}/replies
func (s *Server) handleListReplies(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ticket ID")
		return
	}
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
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ticket ID")
		return
	}
	var body struct {
		Notes string `json:"notes"`
	}
	_ = DecodeJSON(r, &body)

	if !s.requireUserIdentity(w, r) {
		return
	}
	actor := ticket.Actor{UserID: &a.UserID, Role: a.Role}
	t, err := s.tickets.Resolve(r.Context(), id, body.Notes, actor)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, t)
}

// POST /api/v1/tickets/{id}/reopen
func (s *Server) handleReopenTicket(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	if a.Role == user.RoleUser {
		Error(w, http.StatusForbidden, "forbidden", "users cannot directly reopen tickets")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ticket ID")
		return
	}

	statuses, err := s.tickets.ListStatuses(r.Context())
	if err != nil {
		handleError(w, err)
		return
	}
	targetName := s.adminSvc.ReopenTargetStatusName(r.Context())
	var targetID uuid.UUID
	for _, st := range statuses {
		if st.Name == targetName {
			targetID = st.ID
			break
		}
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
	JSON(w, http.StatusOK, t)
}

// POST /api/v1/tickets/{id}/close
func (s *Server) handleCloseTicket(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	if a.Role != user.RoleAdmin {
		Error(w, http.StatusForbidden, "forbidden", "only admins can close tickets")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ticket ID")
		return
	}
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
	JSON(w, http.StatusOK, t)
}

// POST /api/v1/tickets/{id}/links
func (s *Server) handleAddLink(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	sourceID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ticket ID")
		return
	}
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
		Error(w, http.StatusForbidden, "forbidden", "not your ticket")
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
		JSON(w, http.StatusOK, t)
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
	sourceID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ticket ID")
		return
	}
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
		Error(w, http.StatusForbidden, "forbidden", "not your ticket")
		return
	}

	// And it is a staff decision to make. handleAddLink is reachable by a
	// reporter only for two tickets they own; removal had no role check at
	// all, so staff classification on a reporter's own ticket was theirs to
	// undo.
	if a := authmw.GetActor(r); a == nil || (a.Role != user.RoleAdmin && a.Role != user.RoleStaff) {
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
	if err := s.tickets.RemoveLink(r.Context(), sourceID, targetID, lt); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/v1/tickets/{id}/history
func (s *Server) handleListStatusHistory(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ticket ID")
		return
	}
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
			Error(w, http.StatusForbidden, "forbidden", "not your ticket")
			return
		}
	}
	history, err := s.tickets.ListStatusHistory(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, history)
}

// GET /api/v1/tickets/{id}/links
func (s *Server) handleListLinks(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ticket ID")
		return
	}
	links, err := s.tickets.ListLinks(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, links)
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
// Shared by the authenticated reply path and the guest one, because a guest
// reopening a ticket has to land on the same status a reporter would.
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
	for _, st := range statuses {
		if st.Name == want {
			return st.ID, nil
		}
	}
	for _, st := range statuses {
		if st.Name == ticket.StatusNameNew {
			slog.WarnContext(ctx, "configured reopen target status does not exist; falling back to New",
				"configured", want)
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
// Staff are not held to this: reclassifying an old ticket into the category
// it actually belongs to is ordinary work, and that category may well be
// archived.
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
