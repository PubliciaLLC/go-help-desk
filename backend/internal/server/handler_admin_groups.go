package server

import (
	"log/slog"
	"net/http"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// Admin group management: groups, their members, and their CTI scopes.
//
// Split out of handler_admin.go, which had grown to 1,332 lines across twelve
// unrelated resources. Moved verbatim: no handler logic changed.

// ── Groups ───────────────────────────────────────────────────────────────────

func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.groups.List(r.Context())
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, groups)
}

func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	g, err := s.groups.Create(r.Context(), body.Name, body.Description)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusCreated, g)
}

func (s *Server) handleGetGroup(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid group ID")
		return
	}
	g, err := s.groups.GetByID(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, g)
}

func (s *Server) handleUpdateGroup(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid group ID")
		return
	}
	existing, err := s.groups.GetByID(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	var body struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if body.Name != nil {
		existing.Name = *body.Name
	}
	if body.Description != nil {
		existing.Description = *body.Description
	}
	if err := s.groups.Update(r.Context(), existing); err != nil {
		handleError(w, err)
		return
	}
	// Answer with the row as stored: Update trims the name, so echoing
	// `existing` returned a name the database does not hold.
	updated, err := s.groups.GetByID(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid group ID")
		return
	}
	// The group's tickets go back to the queue in the same transaction as the
	// delete, one audit entry each (#419). s.groups.Delete on its own let the
	// foreign key clear them with nothing in the trail.
	var actorID *uuid.UUID
	if actor := authmw.GetActor(r); actor != nil {
		actorID = &actor.UserID
	}
	moved, err := s.tickets.DeleteGroup(r.Context(), actorID, id)
	if err != nil {
		handleError(w, err)
		return
	}
	if moved > 0 {
		slog.InfoContext(r.Context(), "returned the deleted group's tickets to the queue",
			"group_id", id, "tickets", moved)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAddGroupMember(w http.ResponseWriter, r *http.Request) {
	groupID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid group ID")
		return
	}
	var body struct {
		UserID uuid.UUID `json:"user_id"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if body.UserID == uuid.Nil {
		Error(w, http.StatusBadRequest, "bad_request", "user_id is required")
		return
	}
	if err := s.groups.AddMember(r.Context(), groupID, body.UserID); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListGroupMembers(w http.ResponseWriter, r *http.Request) {
	groupID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid group ID")
		return
	}
	// An unknown group is a 404, not an empty list (#418).
	if _, err := s.groups.GetByID(r.Context(), groupID); err != nil {
		handleError(w, err)
		return
	}
	memberIDs, err := s.groups.ListMembers(r.Context(), groupID)
	if err != nil {
		handleError(w, err)
		return
	}
	members := make([]user.User, 0, len(memberIDs))
	for _, uid := range memberIDs {
		u, err := s.users.GetByID(r.Context(), uid)
		if err != nil {
			continue // skip deleted users
		}
		members = append(members, u)
	}
	JSON(w, http.StatusOK, members)
}

func (s *Server) handleRemoveGroupMember(w http.ResponseWriter, r *http.Request) {
	groupID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid group ID")
		return
	}
	userID, err := uuid.Parse(chi.URLParam(r, "userId"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid user ID")
		return
	}
	if err := s.groups.RemoveMember(r.Context(), groupID, userID); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListGroupScopes(w http.ResponseWriter, r *http.Request) {
	groupID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid group ID")
		return
	}
	if _, err := s.groups.GetByID(r.Context(), groupID); err != nil {
		handleError(w, err)
		return
	}
	scopes, err := s.groups.ListScopes(r.Context(), groupID)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, scopes)
}

func (s *Server) handleAddGroupScope(w http.ResponseWriter, r *http.Request) {
	groupID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid group ID")
		return
	}
	var body struct {
		CategoryID uuid.UUID  `json:"category_id"`
		TypeID     *uuid.UUID `json:"type_id"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if body.CategoryID == uuid.Nil {
		Error(w, http.StatusBadRequest, "bad_request", "category_id is required")
		return
	}
	if err := s.groups.AddScope(r.Context(), group.GroupScope{
		GroupID:    groupID,
		CategoryID: body.CategoryID,
		TypeID:     body.TypeID,
	}); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRemoveGroupScope(w http.ResponseWriter, r *http.Request) {
	groupID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid group ID")
		return
	}
	var body struct {
		CategoryID uuid.UUID  `json:"category_id"`
		TypeID     *uuid.UUID `json:"type_id"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if err := s.groups.RemoveScope(r.Context(), groupID, body.CategoryID, body.TypeID); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListGroupsForCategory returns groups with a category-level scope entry
// (type_id IS NULL) — used by the CTI editor to show which groups handle this category.
func (s *Server) handleListGroupsForCategory(w http.ResponseWriter, r *http.Request) {
	catID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid category id")
		return
	}
	groups, err := s.groups.ListGroupsForExactScope(r.Context(), catID, nil)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, groups)
}

// handleListGroupsForType returns groups with a type-specific scope entry
// — used by the CTI editor to show which groups handle this category+type.
func (s *Server) handleListGroupsForType(w http.ResponseWriter, r *http.Request) {
	catID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid category id")
		return
	}
	typeID, err := uuid.Parse(chi.URLParam(r, "typeId"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid type id")
		return
	}
	groups, err := s.groups.ListGroupsForExactScope(r.Context(), catID, &typeID)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, groups)
}
