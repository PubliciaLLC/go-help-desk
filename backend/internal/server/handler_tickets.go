package server

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/ticket"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/user"
	authmw "github.com/open-help-desk/open-help-desk/backend/internal/middleware"
)

// GET /api/v1/tickets
// Returns tickets relevant to the current user:
//   - admin/staff: tickets assigned to them + tickets assigned to any of their groups
//   - user: tickets they reported
//
// Optional query param: assignee_group_id=<uuid> — returns tickets for a specific group
// (requires staff or admin role).
func (s *Server) handleListTickets(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))

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
		var tickets []ticket.Ticket
		if q != "" {
			tickets, err = s.tickets.SearchByAssigneeGroup(ctx, gid, q, 100, 0)
		} else {
			tickets, err = s.tickets.ListByAssigneeGroup(ctx, gid, 100, 0)
		}
		if err != nil {
			handleError(w, err)
			return
		}
		JSON(w, http.StatusOK, tickets)
		return
	}

	// Users only see their own reported tickets.
	if a.Role == user.RoleUser {
		var (
			tickets []ticket.Ticket
			err     error
		)
		if q != "" {
			tickets, err = s.tickets.SearchByReporter(ctx, a.UserID, q, 100, 0)
		} else {
			tickets, err = s.tickets.ListByReporter(ctx, a.UserID, 100, 0)
		}
		if err != nil {
			handleError(w, err)
			return
		}
		JSON(w, http.StatusOK, tickets)
		return
	}

	// Staff/admin: tickets assigned to them + tickets assigned to their groups.
	var all []ticket.Ticket

	var err error
	var mine []ticket.Ticket
	if q != "" {
		mine, err = s.tickets.SearchByAssigneeUser(ctx, a.UserID, q, 100, 0)
	} else {
		mine, err = s.tickets.ListByAssigneeUser(ctx, a.UserID, 100, 0)
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
			gTickets, err = s.tickets.SearchByAssigneeGroup(ctx, g.ID, q, 100, 0)
		} else {
			gTickets, err = s.tickets.ListByAssigneeGroup(ctx, g.ID, 100, 0)
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

	JSON(w, http.StatusOK, all)
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
		in.ReporterUserID = &a.UserID
	} else {
		email := body.GuestEmail
		in.GuestEmail = &email
		in.GuestName = strings.TrimSpace(body.GuestName)
		in.GuestPhone = strings.TrimSpace(body.GuestPhone)
	}

	t, err := s.tickets.Create(r.Context(), in)
	if err != nil {
		handleError(w, err)
		return
	}

	// Set any custom field values supplied on creation (best-effort; skip invalid IDs).
	for fieldDefIDStr, value := range body.CustomFields {
		if value == "" {
			continue
		}
		fieldDefID, parseErr := uuid.Parse(fieldDefIDStr)
		if parseErr != nil {
			continue
		}
		_ = s.customFields.SetValue(r.Context(), t.ID, fieldDefID, value)
	}

	JSON(w, http.StatusCreated, t)
}

// GET /api/v1/tickets/{id}
func (s *Server) handleGetTicket(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
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

	// Users can only view their own tickets.
	if a != nil && a.Role == user.RoleUser {
		if t.ReporterUserID == nil || *t.ReporterUserID != a.UserID {
			Error(w, http.StatusForbidden, "forbidden", "not your ticket")
			return
		}
	}

	JSON(w, http.StatusOK, t)
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
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}

	actor := ticket.Actor{UserID: &a.UserID, Role: a.Role}

	if body.StatusID != nil {
		if _, err := s.tickets.UpdateStatus(r.Context(), id, *body.StatusID, actor); err != nil {
			handleError(w, err)
			return
		}
	}
	if body.AssigneeUserID != nil || body.AssigneeGroupID != nil {
		if _, err := s.tickets.Assign(r.Context(), id, body.AssigneeUserID, body.AssigneeGroupID, actor); err != nil {
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
	reopenStatusName := s.adminSvc.ReopenTargetStatusName(r.Context())

	// Look up the reopen target status ID.
	statuses, err := s.tickets.ListStatuses(r.Context())
	if err != nil {
		handleError(w, err)
		return
	}
	var reopenStatusID uuid.UUID
	for _, st := range statuses {
		if st.Name == reopenStatusName {
			reopenStatusID = st.ID
			break
		}
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
	JSON(w, http.StatusOK, replies)
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

	actor := ticket.Actor{UserID: &a.UserID, Role: a.Role}
	t, err := s.tickets.Reopen(r.Context(), id, targetID, actor)
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
		TargetID uuid.UUID `json:"target_id"`
		LinkType string    `json:"link_type"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	actor := ticket.Actor{UserID: &a.UserID, Role: a.Role}
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
	lt := ticket.LinkType(chi.URLParam(r, "linkType"))
	if err := s.tickets.RemoveLink(r.Context(), sourceID, targetID, lt); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/v1/tickets/{id}/history
func (s *Server) handleListStatusHistory(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ticket ID")
		return
	}
	// Users may only view history for their own tickets.
	if a != nil && a.Role == user.RoleUser {
		t, err := s.tickets.GetByID(r.Context(), id)
		if err != nil {
			handleError(w, err)
			return
		}
		if t.ReporterUserID == nil || *t.ReporterUserID != a.UserID {
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
