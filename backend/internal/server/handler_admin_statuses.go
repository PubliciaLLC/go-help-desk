package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// Admin ticket-status management, including the three system statuses.
//
// Split out of handler_admin.go, which had grown to 1,332 lines across twelve
// unrelated resources. Moved verbatim: no handler logic changed.

// ── Statuses ─────────────────────────────────────────────────────────────────

func (s *Server) handleListStatuses(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	statuses, err := s.tickets.ListStatuses(ctx)
	if err != nil {
		handleError(w, err)
		return
	}

	// Ticket counts are scoped to the caller:
	//   - admin: every ticket (also drives the admin statuses page hard-delete check)
	//   - staff: tickets assigned to them or any of their groups
	//   - user:  tickets they reported
	a := authmw.GetActor(r)
	var groupIDs []uuid.UUID
	if a != nil && a.Role == user.RoleStaff {
		groups, err := s.groups.ListGroupsForUser(ctx, a.UserID)
		if err != nil {
			handleError(w, err)
			return
		}
		groupIDs = make([]uuid.UUID, len(groups))
		for i, g := range groups {
			groupIDs[i] = g.ID
		}
	}

	for i := range statuses {
		var count int64
		var cerr error
		switch {
		case a == nil || a.Role == user.RoleAdmin:
			count, cerr = s.tickets.CountByStatus(ctx, statuses[i].ID)
		case a.Role == user.RoleStaff:
			count, cerr = s.tickets.CountByStatusForAssignee(ctx, statuses[i].ID, a.UserID, groupIDs)
		default:
			count, cerr = s.tickets.CountByStatusForReporter(ctx, statuses[i].ID, a.UserID)
		}
		if cerr == nil {
			statuses[i].TicketCount = count
		}
	}
	JSON(w, http.StatusOK, statuses)
}

func (s *Server) handleCreateStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      string `json:"name"`
		SortOrder int    `json:"sort_order"`
		Color     string `json:"color"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	st := ticket.Status{
		ID:        uuid.New(),
		Name:      body.Name,
		Kind:      ticket.StatusKindCustom,
		SortOrder: body.SortOrder,
		Color:     body.Color,
	}
	if err := s.tickets.AddStatus(r.Context(), st); err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusCreated, st)
}

func (s *Server) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid status ID")
		return
	}
	var body struct {
		Name      *string `json:"name"`
		SortOrder *int    `json:"sort_order"`
		Color     *string `json:"color"`
		Active    *bool   `json:"active"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	statuses, err := s.tickets.ListStatuses(r.Context())
	if err != nil {
		handleError(w, err)
		return
	}
	var st ticket.Status
	for _, s := range statuses {
		if s.ID == id {
			st = s
			break
		}
	}
	if st.ID == uuid.Nil {
		Error(w, http.StatusNotFound, "not_found", "status not found")
		return
	}
	if body.Name != nil {
		st.Name = *body.Name
	}
	if body.SortOrder != nil {
		st.SortOrder = *body.SortOrder
	}
	if body.Color != nil {
		st.Color = *body.Color
	}
	if body.Active != nil {
		if st.Kind == ticket.StatusKindSystem {
			Error(w, http.StatusForbidden, "forbidden", "system statuses cannot be deactivated")
			return
		}
		st.Active = *body.Active
	}
	if err := s.tickets.SaveStatus(r.Context(), st); err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, st)
}

func (s *Server) handleDeleteStatus(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid status ID")
		return
	}
	if err := s.tickets.RemoveStatus(r.Context(), id); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
