package server

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/customfield"
)

// ── Admin: field definitions ──────────────────────────────────────────────────

func (s *Server) handleListFieldDefs(w http.ResponseWriter, r *http.Request) {
	defs, err := s.customFields.ListFieldDefs(r.Context())
	if err != nil {
		handleError(w, err)
		return
	}
	if defs == nil {
		defs = []customfield.FieldDef{}
	}
	JSON(w, http.StatusOK, defs)
}

func (s *Server) handleCreateFieldDef(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      string            `json:"name"`
		FieldType customfield.FieldType `json:"field_type"`
		Options   []string          `json:"options"`
		SortOrder int               `json:"sort_order"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	def, err := s.customFields.CreateFieldDef(r.Context(), body.Name, body.FieldType, body.Options, body.SortOrder)
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	JSON(w, http.StatusCreated, def)
}

func (s *Server) handleUpdateFieldDef(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid field def id")
		return
	}
	existing, err := s.customFields.GetFieldDef(r.Context(), id)
	if err != nil {
		handleFieldDefNotFound(w, err)
		return
	}
	var body struct {
		Name      *string            `json:"name"`
		FieldType *customfield.FieldType `json:"field_type"`
		Options   []string           `json:"options"`
		SortOrder *int               `json:"sort_order"`
		Active    *bool              `json:"active"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if body.Name != nil {
		existing.Name = *body.Name
	}
	if body.FieldType != nil {
		existing.FieldType = *body.FieldType
	}
	if body.Options != nil {
		existing.Options = body.Options
	}
	if body.SortOrder != nil {
		existing.SortOrder = *body.SortOrder
	}
	if body.Active != nil {
		existing.Active = *body.Active
	}
	if err := s.customFields.UpdateFieldDef(r.Context(), existing); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	JSON(w, http.StatusOK, existing)
}

// ── Admin: CTI field assignments ──────────────────────────────────────────────

func (s *Server) handleListAssignments(w http.ResponseWriter, r *http.Request) {
	scopeType, scopeID, ok := parseScopeFromURL(w, r)
	if !ok {
		return
	}
	assignments, err := s.customFields.ListAssignments(r.Context(), scopeType, scopeID)
	if err != nil {
		handleError(w, err)
		return
	}
	if assignments == nil {
		assignments = []customfield.Assignment{}
	}
	JSON(w, http.StatusOK, assignments)
}

func (s *Server) handleCreateAssignment(w http.ResponseWriter, r *http.Request) {
	scopeType, scopeID, ok := parseScopeFromURL(w, r)
	if !ok {
		return
	}
	var body struct {
		FieldDefID    uuid.UUID `json:"field_def_id"`
		SortOrder     int       `json:"sort_order"`
		VisibleOnNew  bool      `json:"visible_on_new"`
		RequiredOnNew bool      `json:"required_on_new"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	a, err := s.customFields.CreateAssignment(r.Context(), body.FieldDefID, scopeType, scopeID, body.SortOrder, body.VisibleOnNew, body.RequiredOnNew)
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	JSON(w, http.StatusCreated, a)
}

func (s *Server) handleUpdateAssignment(w http.ResponseWriter, r *http.Request) {
	assignmentID, err := uuid.Parse(chi.URLParam(r, "assignmentId"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid assignment id")
		return
	}
	existing, err := s.customFields.GetAssignment(r.Context(), assignmentID)
	if err != nil {
		handleFieldDefNotFound(w, err)
		return
	}
	var body struct {
		SortOrder     *int  `json:"sort_order"`
		VisibleOnNew  *bool `json:"visible_on_new"`
		RequiredOnNew *bool `json:"required_on_new"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if body.SortOrder != nil {
		existing.SortOrder = *body.SortOrder
	}
	if body.VisibleOnNew != nil {
		existing.VisibleOnNew = *body.VisibleOnNew
	}
	if body.RequiredOnNew != nil {
		existing.RequiredOnNew = *body.RequiredOnNew
	}
	if err := s.customFields.UpdateAssignment(r.Context(), existing); err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, existing)
}

func (s *Server) handleDeleteAssignment(w http.ResponseWriter, r *http.Request) {
	assignmentID, err := uuid.Parse(chi.URLParam(r, "assignmentId"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid assignment id")
		return
	}
	if err := s.customFields.DeleteAssignment(r.Context(), assignmentID); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Ticket: resolve fields for CTI (used on new-ticket form) ─────────────────

func (s *Server) handleResolveFieldsForCTI(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	categoryID, err := uuid.Parse(q.Get("category_id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "category_id is required")
		return
	}
	var typeID *uuid.UUID
	if raw := q.Get("type_id"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			Error(w, http.StatusBadRequest, "bad_request", "invalid type_id")
			return
		}
		typeID = &parsed
	}
	var itemID *uuid.UUID
	if raw := q.Get("item_id"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			Error(w, http.StatusBadRequest, "bad_request", "invalid item_id")
			return
		}
		itemID = &parsed
	}
	fields, err := s.customFields.ResolveFieldsForCTI(r.Context(), categoryID, typeID, itemID)
	if err != nil {
		handleError(w, err)
		return
	}
	if fields == nil {
		fields = []customfield.Assignment{}
	}
	JSON(w, http.StatusOK, fields)
}

// ── Ticket: custom field values ───────────────────────────────────────────────

func (s *Server) handleListTicketCustomFields(w http.ResponseWriter, r *http.Request) {
	ticketID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ticket id")
		return
	}
	values, err := s.customFields.ListValuesForTicket(r.Context(), ticketID)
	if err != nil {
		handleError(w, err)
		return
	}
	if values == nil {
		values = []customfield.TicketFieldValue{}
	}
	JSON(w, http.StatusOK, values)
}

func (s *Server) handlePutTicketCustomFields(w http.ResponseWriter, r *http.Request) {
	ticketID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ticket id")
		return
	}
	// Body: map of fieldDefId (string) → value (string)
	var body map[string]string
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	for fieldDefIDStr, value := range body {
		fieldDefID, err := uuid.Parse(fieldDefIDStr)
		if err != nil {
			Error(w, http.StatusBadRequest, "bad_request", "invalid field_def_id: "+fieldDefIDStr)
			return
		}
		if err := s.customFields.SetValue(r.Context(), ticketID, fieldDefID, value); err != nil {
			Error(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── helpers ───────────────────────────────────────────────────────────────────

// parseScopeFromURL extracts scope_type and scope_id from the URL params.
// For category-level: uses {id} as scope_id, scope_type="category".
// For type-level: uses {typeId} as scope_id, scope_type="type".
// For item-level: uses {itemId} as scope_id, scope_type="item".
func parseScopeFromURL(w http.ResponseWriter, r *http.Request) (customfield.ScopeType, uuid.UUID, bool) {
	if itemIDStr := chi.URLParam(r, "itemId"); itemIDStr != "" {
		id, err := uuid.Parse(itemIDStr)
		if err != nil {
			Error(w, http.StatusBadRequest, "bad_request", "invalid item id")
			return "", uuid.Nil, false
		}
		return customfield.ItemScope, id, true
	}
	if typeIDStr := chi.URLParam(r, "typeId"); typeIDStr != "" {
		id, err := uuid.Parse(typeIDStr)
		if err != nil {
			Error(w, http.StatusBadRequest, "bad_request", "invalid type id")
			return "", uuid.Nil, false
		}
		return customfield.TypeScope, id, true
	}
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid category id")
		return "", uuid.Nil, false
	}
	return customfield.CategoryScope, id, true
}

func handleFieldDefNotFound(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		Error(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	handleError(w, err)
}
