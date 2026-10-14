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
	count, err := s.statusCounter(r)
	if err != nil {
		handleError(w, err)
		return
	}
	for i := range statuses {
		// A count that failed fails the request. It used to leave 0 in place,
		// which the admin page reads as "no tickets, safe to hard-delete".
		if statuses[i].TicketCount, err = count(statuses[i].ID); err != nil {
			handleError(w, err)
			return
		}
	}
	JSON(w, http.StatusOK, statuses)
}

// statusCounter returns the ticket count for a status as the caller may see it:
//   - admin: every ticket (also drives the admin statuses page hard-delete check)
//   - staff: tickets assigned to them or any of their groups
//   - user:  tickets they reported
func (s *Server) statusCounter(r *http.Request) (func(uuid.UUID) (int64, error), error) {
	ctx := r.Context()
	a := authmw.GetActor(r)
	var groupIDs []uuid.UUID
	if a != nil && a.Role == user.RoleStaff {
		groups, err := s.groups.ListGroupsForUser(ctx, a.UserID)
		if err != nil {
			return nil, err
		}
		groupIDs = make([]uuid.UUID, len(groups))
		for i, g := range groups {
			groupIDs[i] = g.ID
		}
	}
	return func(statusID uuid.UUID) (int64, error) {
		switch {
		case a == nil || a.Role == user.RoleAdmin:
			return s.tickets.CountByStatus(ctx, statusID)
		case a.Role == user.RoleStaff:
			return s.tickets.CountByStatusForAssignee(ctx, statusID, a.UserID, groupIDs)
		default:
			return s.tickets.CountByStatusForReporter(ctx, statusID, a.UserID)
		}
	}, nil
}

// statusWithCount fills in the count for the one status a POST or PATCH
// answers with, so the response has the shape the list has.
func (s *Server) statusWithCount(r *http.Request, st ticket.Status) (ticket.Status, error) {
	count, err := s.statusCounter(r)
	if err != nil {
		return st, err
	}
	st.TicketCount, err = count(st.ID)
	return st, err
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
	saved, err := s.tickets.AddStatus(r.Context(), st)
	if err != nil {
		handleError(w, err)
		return
	}
	if saved, err = s.statusWithCount(r, saved); err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusCreated, saved)
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
		// System statuses are found by name: LoadSystemStatuses fails startup
		// if New/Resolved/Closed is missing, and lifecycle rules compare
		// against those names. Renaming one broke the next restart (#263) and
		// is the gap migration 000028's #230 guard was written around.
		// Resending the unchanged name is not a rename.
		//
		// The refusal itself now lives entirely in SaveStatus (#269): it
		// returns ticket.ErrSystemStatusImmutable, which handleError maps to
		// this same 403, so there is no need to duplicate the Kind/name
		// check at this layer too.
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
			// This is the one inline refusal handleUpdateStatus still has of
			// its own; the rename check above it was removed by #269. Because
			// Name is applied to st unconditionally above, before this check
			// runs, a request that both renames and deactivates a system
			// status (e.g. {"name":"Done","active":false}) never reaches
			// SaveStatus and its rename refusal at all: the caller gets this
			// 403 "cannot be deactivated" instead of "cannot be renamed",
			// the reverse of what the same request got before #269. Same
			// status code and error code either way, just a different
			// message — see #272.
			Error(w, http.StatusForbidden, "forbidden", "system statuses cannot be deactivated")
			return
		}
		st.Active = *body.Active
	}
	saved, err := s.tickets.SaveStatus(r.Context(), st)
	if err != nil {
		handleError(w, err)
		return
	}
	if saved, err = s.statusWithCount(r, saved); err != nil {
		handleError(w, err)
		return
	}
	// Deactivating the status reopened tickets are sent to leaves them going
	// to New. Say so now, while the administrator is looking at it.
	reopen, err := s.reopenTargetInactive(r.Context())
	if err != nil {
		handleError(w, err)
		return
	}
	if reopen.Inactive && reopen.Name == saved.Name {
		JSON(w, http.StatusOK, struct {
			ticket.Status
			Warning string `json:"warning"`
		}{saved, reopenTargetWarning(saved.Name)})
		return
	}
	JSON(w, http.StatusOK, saved)
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
