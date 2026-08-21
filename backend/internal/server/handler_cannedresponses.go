package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/cannedresponse"
)

// cannedResponseBody is the JSON shape accepted by create/update requests.
type cannedResponseBody struct {
	Name       string     `json:"name"`
	Body       string     `json:"body"`
	CategoryID *uuid.UUID `json:"category_id"`
	TypeID     *uuid.UUID `json:"type_id"`
	SortOrder  int        `json:"sort_order"`
}

// ── Admin canned response handlers ────────────────────────────────────────────

// handleAdminListCannedResponses returns all canned responses.
func (s *Server) handleAdminListCannedResponses(w http.ResponseWriter, r *http.Request) {
	crs, err := s.cannedResponses.List(r.Context())
	if err != nil {
		Error(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	JSON(w, http.StatusOK, crs)
}

// handleAdminCreateCannedResponse creates a new canned response.
func (s *Server) handleAdminCreateCannedResponse(w http.ResponseWriter, r *http.Request) {
	var body cannedResponseBody
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	cr, err := s.cannedResponses.Create(r.Context(), body.Name, body.Body, body.CategoryID, body.TypeID, body.SortOrder)
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	JSON(w, http.StatusCreated, cr)
}

// handleAdminUpdateCannedResponse updates an existing canned response.
func (s *Server) handleAdminUpdateCannedResponse(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid_id", "invalid canned response id")
		return
	}
	var body cannedResponseBody
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	cr := cannedresponse.CannedResponse{
		ID:         id,
		Name:       body.Name,
		Body:       body.Body,
		CategoryID: body.CategoryID,
		TypeID:     body.TypeID,
		SortOrder:  body.SortOrder,
	}
	if err := s.cannedResponses.Update(r.Context(), cr); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	JSON(w, http.StatusOK, cr)
}

// handleAdminDeleteCannedResponse deletes a canned response.
func (s *Server) handleAdminDeleteCannedResponse(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid_id", "invalid canned response id")
		return
	}
	if err := s.cannedResponses.Delete(r.Context(), id); err != nil {
		Error(w, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Ticket-scoped canned responses ────────────────────────────────────────────

// handleListTicketCannedResponses returns the canned responses available for
// the ticket's category and type — the reply-composer picker.
func (s *Server) handleListTicketCannedResponses(w http.ResponseWriter, r *http.Request) {
	ticketID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid_id", "invalid ticket id")
		return
	}
	t, err := s.tickets.GetByID(r.Context(), ticketID)
	if err != nil {
		handleError(w, err)
		return
	}
	crs, err := s.cannedResponses.Available(r.Context(), t.CategoryID, t.TypeID)
	if err != nil {
		Error(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	JSON(w, http.StatusOK, crs)
}
