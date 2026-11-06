package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
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

// fieldAdminContext is the request's context carrying who is changing a field,
// for the audit entries the change writes in its own transaction. A credential
// with no person behind it is audited as the system (actor_id is a foreign key
// to users, and the nil UUID names nobody).
func fieldAdminContext(r *http.Request) context.Context {
	if a := authmw.GetActor(r); a != nil && a.UserID != uuid.Nil {
		return customfield.WithActor(r.Context(), a.UserID)
	}
	return r.Context()
}

// actorIDOf is the same person as a pointer, for the calls that take one.
func actorIDOf(r *http.Request) *uuid.UUID {
	if a := authmw.GetActor(r); a != nil && a.UserID != uuid.Nil {
		return &a.UserID
	}
	return nil
}

func (s *Server) handleCreateFieldDef(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      string                `json:"name"`
		FieldType customfield.FieldType `json:"field_type"`
		Options   []string              `json:"options"`
		SortOrder int                   `json:"sort_order"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	def, err := s.customFields.CreateFieldDef(fieldAdminContext(r), body.Name, body.FieldType, body.Options, body.SortOrder)
	if err != nil {
		handleError(w, err)
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
	var body struct {
		Name      *string                `json:"name"`
		FieldType *customfield.FieldType `json:"field_type"`
		Options   []string               `json:"options"`
		// Retired options are moved here from options, never added: a value
		// that was not an option cannot be retired. Omitted means no change;
		// an empty list clears them (deleting the ones nobody holds).
		RetiredOptions []string `json:"retired_options"`
		SortOrder      *int     `json:"sort_order"`
		Active         *bool    `json:"active"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	// Applied to the row as it is under the lock the service takes, not to a
	// copy read here: an option delete or a type change is decided against what
	// tickets hold at that moment.
	saved, err := s.customFields.PatchFieldDef(fieldAdminContext(r), id, customfield.FieldDefPatch{
		Name: body.Name, FieldType: body.FieldType, Options: body.Options, RetiredOptions: body.RetiredOptions,
		SortOrder: body.SortOrder, Active: body.Active,
	})
	if err != nil {
		handleFieldDefNotFound(w, err)
		return
	}
	JSON(w, http.StatusOK, saved)
}

// handleReorderFieldDefs sets every field's number from the order of the list.
func (s *Server) handleReorderFieldDefs(w http.ResponseWriter, r *http.Request) {
	var body struct {
		// A pointer: a missing key and null are 400, an empty list is not.
		FieldDefIDs *[]uuid.UUID `json:"field_def_ids"`
	}
	if err := DecodeJSON(r, &body); err != nil || body.FieldDefIDs == nil {
		Error(w, http.StatusBadRequest, "bad_request", "field_def_ids is required: a list of field ids")
		return
	}
	if err := s.customFields.ReorderFieldDefs(fieldAdminContext(r), *body.FieldDefIDs); err != nil {
		handleError(w, err)
		return
	}
	s.handleListFieldDefs(w, r)
}

// handleFieldDefUsage is how many tickets hold a value for the field, and each
// of its values.
func (s *Server) handleFieldDefUsage(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid field def id")
		return
	}
	usage, err := s.customFields.Usage(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, usage)
}

// handleListFieldPlacements is where the field is attached, with the names.
func (s *Server) handleListFieldPlacements(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid field def id")
		return
	}
	places, err := s.customFields.ListPlacements(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	if places == nil {
		places = []customfield.Placement{}
	}
	JSON(w, http.StatusOK, places)
}

// ── Admin: CTI field assignments ──────────────────────────────────────────────

func (s *Server) handleListAssignments(w http.ResponseWriter, r *http.Request) {
	p, ok := s.resolveCTIPath(w, r)
	if !ok {
		return
	}
	scopeType, scopeID := p.scope()
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
	p, ok := s.resolveCTIPath(w, r)
	if !ok {
		return
	}
	scopeType, scopeID := p.scope()
	var body struct {
		FieldDefID    uuid.UUID `json:"field_def_id"`
		SortOrder     int       `json:"sort_order"`
		VisibleOnNew  *bool     `json:"visible_on_new"`
		RequiredOnNew bool      `json:"required_on_new"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	// Omitted means false, which is what this API always did. A pointer so the
	// default is written here rather than being whatever the zero value of the
	// field type happens to be; the column default (migration 000040) says the
	// same thing to anything that inserts without going through here.
	visibleOnNew := false
	if body.VisibleOnNew != nil {
		visibleOnNew = *body.VisibleOnNew
	}
	a, err := s.customFields.CreateAssignment(fieldAdminContext(r), body.FieldDefID, scopeType, scopeID, body.SortOrder, visibleOnNew, body.RequiredOnNew)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusCreated, a)
}

func (s *Server) handleUpdateAssignment(w http.ResponseWriter, r *http.Request) {
	existing, ok := s.assignmentInURL(w, r)
	if !ok {
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
	if err := s.customFields.UpdateAssignment(fieldAdminContext(r), existing); err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, existing)
}

func (s *Server) handleDeleteAssignment(w http.ResponseWriter, r *http.Request) {
	existing, ok := s.assignmentInURL(w, r)
	if !ok {
		return
	}
	if err := s.customFields.DeleteAssignment(fieldAdminContext(r), existing.ID); err != nil {
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
	ticketID := ticketIDFrom(r)
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
	ticketID := ticketIDFrom(r)
	// Body: map of fieldDefId (string) → value (string)
	var body map[string]string
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	// A Closed ticket is read-only to its requester (#349). One rule, in the
	// ticket service, asked here and nowhere re-spelled: a reporting user may
	// edit a field on their own ticket until it closes, and never after.
	// Staff and admin are not refused.
	if a := authmw.GetActor(r); a != nil {
		if err := s.tickets.CanRequesterWrite(r.Context(), ticketID,
			ticket.Actor{UserID: &a.UserID, Role: a.Role}); err != nil {
			handleError(w, err)
			return
		}
	}
	// Which fields this ticket actually has, resolved from its own
	// classification.
	//
	// There was no check at all: any field id reached SetValue and was
	// stored. A reporting user could write a staff-only field — one assigned
	// to no scope, so not rendered anywhere and not offered to anyone — on
	// their own ticket, and DESIGN.md says users see category and type fields
	// only. Everyone is held to it, not just reporters: a value on a field
	// the ticket does not have is a row nothing will ever display, which is
	// not a thing worth writing for any role.
	allowed, err := s.fieldsForTicket(r.Context(), ticketID)
	if err != nil {
		handleError(w, err)
		return
	}

	// All or nothing. Every entry is parsed and held to the check above before
	// anything is written, and SetValues then checks the rest and writes in one
	// transaction. The loop that stood here wrote entry by entry, in map order,
	// so one refused entry left the ones before it saved and the ones after it
	// not, differently on each try.
	values := make(map[uuid.UUID]string, len(body))
	for fieldDefIDStr, value := range body {
		fieldDefID, err := uuid.Parse(fieldDefIDStr)
		if err != nil {
			Error(w, http.StatusBadRequest, "bad_request", "invalid field_def_id: "+fieldDefIDStr)
			return
		}
		if !allowed[fieldDefID] {
			Error(w, http.StatusBadRequest, "bad_request",
				"field_def_id is not a field on this ticket: "+fieldDefIDStr)
			return
		}
		// Two keys can spell one UUID (case, braces); ranging over the map
		// would keep whichever came last, a different one each time.
		if _, dup := values[fieldDefID]; dup {
			Error(w, http.StatusBadRequest, "bad_request", "field_def_id is listed twice: "+fieldDefIDStr)
			return
		}
		values[fieldDefID] = value
	}
	if err := s.customFields.SetValues(r.Context(), ticketID, values); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── helpers ───────────────────────────────────────────────────────────────────

// assignmentInURL loads the assignment a PATCH or DELETE names, and answers
// 404 unless the path's chain is real and the assignment belongs to the scope it
// ends at (#419). Before, only {assignmentId} was read, so
// /categories/X/fields/Z acted on Z whatever scope Z was attached to.
func (s *Server) assignmentInURL(w http.ResponseWriter, r *http.Request) (customfield.Assignment, bool) {
	p, ok := s.resolveCTIPath(w, r)
	if !ok {
		return customfield.Assignment{}, false
	}
	assignmentID, err := uuid.Parse(chi.URLParam(r, "assignmentId"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid assignment id")
		return customfield.Assignment{}, false
	}
	a, err := s.customFields.GetAssignment(r.Context(), assignmentID)
	if err != nil {
		handleFieldDefNotFound(w, err)
		return customfield.Assignment{}, false
	}
	scopeType, scopeID := p.scope()
	if a.ScopeType != scopeType || a.ScopeID != scopeID {
		handleError(w, fmt.Errorf("assignment %s is not on this %s: %w", a.ID, scopeType, customfield.ErrNotFound))
		return customfield.Assignment{}, false
	}
	return a, true
}

func handleFieldDefNotFound(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		Error(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	handleError(w, err)
}

// fieldsForTicket is the set of custom fields a ticket has, resolved from its
// own category, type and item.
//
// The same resolution the ticket page renders from, so what can be written is
// what can be seen. Anything else is a value stored against a field the
// ticket does not have — invisible, unreportable, and on a field an
// administrator may have scoped deliberately narrowly.
func (s *Server) fieldsForTicket(ctx context.Context, ticketID uuid.UUID) (map[uuid.UUID]bool, error) {
	t, err := s.tickets.GetByID(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	assignments, err := s.customFields.ResolveFieldsForCTI(ctx, t.CategoryID, t.TypeID, t.ItemID)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]bool, len(assignments))
	for _, a := range assignments {
		out[a.FieldDefID] = true
	}
	return out, nil
}
