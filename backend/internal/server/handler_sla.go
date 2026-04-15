package server

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/sla"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/ticket"
)

// GET /admin/sla/policies
func (s *Server) handleListSLAPolicies(w http.ResponseWriter, r *http.Request) {
	policies, err := s.slaPolicies.ListPolicies(r.Context())
	if err != nil {
		handleError(w, err)
		return
	}
	if policies == nil {
		policies = []sla.Policy{}
	}
	JSON(w, http.StatusOK, policies)
}

// POST /admin/sla/policies
func (s *Server) handleCreateSLAPolicy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name                string     `json:"name"`
		Priority            string     `json:"priority"`
		CategoryID          *uuid.UUID `json:"category_id"`
		ResponseTargetMin   int        `json:"response_target_min"`
		ResolutionTargetMin int        `json:"resolution_target_min"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	p, err := s.slaPolicies.CreatePolicy(r.Context(), sla.Policy{
		Name:                body.Name,
		Priority:            ticket.Priority(body.Priority),
		CategoryID:          body.CategoryID,
		ResponseTargetMin:   body.ResponseTargetMin,
		ResolutionTargetMin: body.ResolutionTargetMin,
	})
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	JSON(w, http.StatusCreated, p)
}

// PATCH /admin/sla/policies/{id}
func (s *Server) handleUpdateSLAPolicy(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	existing, err := s.slaPolicies.GetPolicy(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			Error(w, http.StatusNotFound, "not_found", "policy not found")
		} else {
			handleError(w, err)
		}
		return
	}
	var body struct {
		Name                *string    `json:"name"`
		Priority            *string    `json:"priority"`
		CategoryID          *uuid.UUID `json:"category_id"`
		ClearCategory       bool       `json:"clear_category"`
		ResponseTargetMin   *int       `json:"response_target_min"`
		ResolutionTargetMin *int       `json:"resolution_target_min"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if body.Name != nil {
		existing.Name = *body.Name
	}
	if body.Priority != nil {
		existing.Priority = ticket.Priority(*body.Priority)
	}
	if body.ClearCategory {
		existing.CategoryID = nil
	} else if body.CategoryID != nil {
		existing.CategoryID = body.CategoryID
	}
	if body.ResponseTargetMin != nil {
		existing.ResponseTargetMin = *body.ResponseTargetMin
	}
	if body.ResolutionTargetMin != nil {
		existing.ResolutionTargetMin = *body.ResolutionTargetMin
	}
	if err := s.slaPolicies.UpdatePolicy(r.Context(), existing); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	JSON(w, http.StatusOK, existing)
}

// DELETE /admin/sla/policies/{id}
func (s *Server) handleDeleteSLAPolicy(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	if err := s.slaPolicies.DeletePolicy(r.Context(), id); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
