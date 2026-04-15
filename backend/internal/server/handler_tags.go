package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/tag"
)

// ── Admin tag handlers ────────────────────────────────────────────────────────

// handleAdminCreateTag creates a new tag by name (lowercased). Returns 409 if
// a tag with that name already exists (active or deleted).
func (s *Server) handleAdminCreateTag(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		Error(w, http.StatusBadRequest, "bad_request", "name is required")
		return
	}
	t, err := s.tags.Resolve(r.Context(), body.Name)
	if err != nil {
		if errors.Is(err, tag.ErrDeleted) {
			Error(w, http.StatusConflict, "tag_deleted", "a deactivated tag with this name already exists; restore it instead")
			return
		}
		handleError(w, err)
		return
	}
	JSON(w, http.StatusCreated, t)
}

// handleAdminListTags returns all tags (including deleted) for the admin panel.
func (s *Server) handleAdminListTags(w http.ResponseWriter, r *http.Request) {
	tags, err := s.tags.ListAll(r.Context())
	if err != nil {
		Error(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	JSON(w, http.StatusOK, tags)
}

// handleAdminDeleteTag soft-deletes a tag.
func (s *Server) handleAdminDeleteTag(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid_id", "invalid tag id")
		return
	}
	if err := s.tags.Delete(r.Context(), id); err != nil {
		Error(w, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAdminRestoreTag undeletes a tag.
func (s *Server) handleAdminRestoreTag(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid_id", "invalid tag id")
		return
	}
	if err := s.tags.Restore(r.Context(), id); err != nil {
		Error(w, http.StatusInternalServerError, "restore_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Active tags (autocomplete) ────────────────────────────────────────────────

// handleListActiveTags returns active tags, optionally filtered by ?q= prefix.
func (s *Server) handleListActiveTags(w http.ResponseWriter, r *http.Request) {
	prefix := r.URL.Query().Get("q")
	tags, err := s.tags.Search(r.Context(), prefix)
	if err != nil {
		Error(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	JSON(w, http.StatusOK, tags)
}

// ── Ticket tag handlers ───────────────────────────────────────────────────────

// handleListTicketTags returns the tags on a ticket.
func (s *Server) handleListTicketTags(w http.ResponseWriter, r *http.Request) {
	ticketID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid_id", "invalid ticket id")
		return
	}
	tags, err := s.tags.ListForTicket(r.Context(), ticketID)
	if err != nil {
		Error(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	JSON(w, http.StatusOK, tags)
}

// handleAddTicketTag adds a tag to a ticket by name, creating the tag if new.
func (s *Server) handleAddTicketTag(w http.ResponseWriter, r *http.Request) {
	ticketID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid_id", "invalid ticket id")
		return
	}

	var body struct {
		Name string `json:"name"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}

	t, err := s.tags.AddToTicket(r.Context(), ticketID, body.Name)
	if err != nil {
		if errors.Is(err, tag.ErrDeleted) {
			Error(w, http.StatusForbidden, "tag_deleted", err.Error())
			return
		}
		Error(w, http.StatusInternalServerError, "add_failed", err.Error())
		return
	}
	JSON(w, http.StatusCreated, t)
}

// handleRemoveTicketTag removes a tag from a ticket.
func (s *Server) handleRemoveTicketTag(w http.ResponseWriter, r *http.Request) {
	ticketID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid_id", "invalid ticket id")
		return
	}
	tagID, err := uuid.Parse(chi.URLParam(r, "tagId"))
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid_id", "invalid tag id")
		return
	}
	if err := s.tags.RemoveFromTicket(r.Context(), ticketID, tagID); err != nil {
		Error(w, http.StatusInternalServerError, "remove_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
