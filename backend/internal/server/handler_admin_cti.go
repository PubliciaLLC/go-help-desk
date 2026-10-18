package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
)

// Admin category/type/item management — the three-level CTI taxonomy.
//
// Split out of handler_admin.go, which had grown to 1,332 lines across twelve
// unrelated resources. Moved verbatim: no handler logic changed.

// ── The path chain ───────────────────────────────────────────────────────────

// ctiPath is the category > type > item chain a URL names. Routes used to read
// only the deepest id, so /categories/X/types/Y acted on Y whatever X was, and
// an assignment could be created for an id that names nothing (#419).
type ctiPath struct {
	category uuid.UUID
	typ      *uuid.UUID // nil above type level
	item     *uuid.UUID // nil above item level
}

// scope is the level the path ends at, which is where a field assignment
// attaches.
func (p ctiPath) scope() (customfield.ScopeType, uuid.UUID) {
	switch {
	case p.item != nil:
		return customfield.ItemScope, *p.item
	case p.typ != nil:
		return customfield.TypeScope, *p.typ
	}
	return customfield.CategoryScope, p.category
}

// resolveCTIPath reads whichever of {id}, {typeId} and {itemId} the route has,
// answers 400 for one that is not a UUID, and 404 unless every id exists and
// hangs off the one before it. It writes the response when it returns false.
func (s *Server) resolveCTIPath(w http.ResponseWriter, r *http.Request) (ctiPath, bool) {
	var p ctiPath
	var err error
	if p.category, err = uuid.Parse(chi.URLParam(r, "id")); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid category ID")
		return ctiPath{}, false
	}
	if raw := chi.URLParam(r, "typeId"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			Error(w, http.StatusBadRequest, "bad_request", "invalid type ID")
			return ctiPath{}, false
		}
		p.typ = &id
	}
	if raw := chi.URLParam(r, "itemId"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			Error(w, http.StatusBadRequest, "bad_request", "invalid item ID")
			return ctiPath{}, false
		}
		p.item = &id
	}
	if err := s.verifyCTIPath(r.Context(), p); err != nil {
		handleError(w, err)
		return ctiPath{}, false
	}
	return p, true
}

func (s *Server) verifyCTIPath(ctx context.Context, p ctiPath) error {
	if _, err := s.categories.GetCategory(ctx, p.category); err != nil {
		return err
	}
	if p.typ == nil {
		return nil
	}
	tp, err := s.categories.GetType(ctx, *p.typ)
	if err != nil {
		return err
	}
	if tp.CategoryID != p.category {
		return fmt.Errorf("type %s is not in category %s: %w", tp.ID, p.category, category.ErrNotFound)
	}
	if p.item == nil {
		return nil
	}
	it, err := s.categories.GetItem(ctx, *p.item)
	if err != nil {
		return err
	}
	if it.TypeID != tp.ID {
		return fmt.Errorf("item %s is not in type %s: %w", it.ID, tp.ID, category.ErrNotFound)
	}
	return nil
}

// ── Categories ───────────────────────────────────────────────────────────────

func (s *Server) handleListCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := s.categories.ListCategories(r.Context(), false)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, cats)
}

func (s *Server) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      string `json:"name"`
		SortOrder int    `json:"sort_order"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	c, err := s.categories.CreateCategory(r.Context(), body.Name, body.SortOrder)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusCreated, c)
}

func (s *Server) handleGetCategory(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ID")
		return
	}
	c, err := s.categories.GetCategory(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, c)
}

func (s *Server) handleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ID")
		return
	}
	existing, err := s.categories.GetCategory(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	var body struct {
		Name      *string `json:"name"`
		SortOrder *int    `json:"sort_order"`
		Active    *bool   `json:"active"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if body.Name != nil {
		existing.Name = *body.Name
	}
	if body.SortOrder != nil {
		existing.SortOrder = *body.SortOrder
	}
	if body.Active != nil {
		existing.Active = *body.Active
	}
	if err := s.categories.UpdateCategory(r.Context(), existing); err != nil {
		handleError(w, err)
		return
	}
	// The saved row, not the edited copy: the name is trimmed on the way in.
	saved, err := s.categories.GetCategory(r.Context(), existing.ID)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, saved)
}

func (s *Server) handleDeleteCategory(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ID")
		return
	}
	if err := s.categories.DeleteCategory(r.Context(), id); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListTypes(w http.ResponseWriter, r *http.Request) {
	catID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid category ID")
		return
	}
	types, err := s.categories.ListTypes(r.Context(), catID, false)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, types)
}

func (s *Server) handleCreateType(w http.ResponseWriter, r *http.Request) {
	catID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid category ID")
		return
	}
	var body struct {
		Name      string `json:"name"`
		SortOrder int    `json:"sort_order"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	tp, err := s.categories.CreateType(r.Context(), catID, body.Name, body.SortOrder)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusCreated, tp)
}

func (s *Server) handleUpdateType(w http.ResponseWriter, r *http.Request) {
	p, ok := s.resolveCTIPath(w, r)
	if !ok {
		return
	}
	existing, err := s.categories.GetType(r.Context(), *p.typ)
	if err != nil {
		handleError(w, err)
		return
	}
	var body struct {
		Name      *string `json:"name"`
		SortOrder *int    `json:"sort_order"`
		Active    *bool   `json:"active"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if body.Name != nil {
		existing.Name = *body.Name
	}
	if body.SortOrder != nil {
		existing.SortOrder = *body.SortOrder
	}
	if body.Active != nil {
		existing.Active = *body.Active
	}
	if err := s.categories.UpdateType(r.Context(), existing); err != nil {
		handleError(w, err)
		return
	}
	// The saved row, not the edited copy: the name is trimmed on the way in.
	saved, err := s.categories.GetType(r.Context(), existing.ID)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, saved)
}

func (s *Server) handleDeleteType(w http.ResponseWriter, r *http.Request) {
	p, ok := s.resolveCTIPath(w, r)
	if !ok {
		return
	}
	if err := s.categories.DeleteType(r.Context(), *p.typ); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListItems(w http.ResponseWriter, r *http.Request) {
	p, ok := s.resolveCTIPath(w, r)
	if !ok {
		return
	}
	items, err := s.categories.ListItems(r.Context(), *p.typ, false)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, items)
}

func (s *Server) handleCreateItem(w http.ResponseWriter, r *http.Request) {
	p, ok := s.resolveCTIPath(w, r)
	if !ok {
		return
	}
	var body struct {
		Name      string `json:"name"`
		SortOrder int    `json:"sort_order"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	it, err := s.categories.CreateItem(r.Context(), *p.typ, body.Name, body.SortOrder)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusCreated, it)
}

func (s *Server) handleUpdateItem(w http.ResponseWriter, r *http.Request) {
	p, ok := s.resolveCTIPath(w, r)
	if !ok {
		return
	}
	existing, err := s.categories.GetItem(r.Context(), *p.item)
	if err != nil {
		handleError(w, err)
		return
	}
	var body struct {
		Name      *string `json:"name"`
		SortOrder *int    `json:"sort_order"`
		Active    *bool   `json:"active"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if body.Name != nil {
		existing.Name = *body.Name
	}
	if body.SortOrder != nil {
		existing.SortOrder = *body.SortOrder
	}
	if body.Active != nil {
		existing.Active = *body.Active
	}
	if err := s.categories.UpdateItem(r.Context(), existing); err != nil {
		handleError(w, err)
		return
	}
	// The saved row, not the edited copy: the name is trimmed on the way in.
	saved, err := s.categories.GetItem(r.Context(), existing.ID)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, saved)
}

func (s *Server) handleDeleteItem(w http.ResponseWriter, r *http.Request) {
	p, ok := s.resolveCTIPath(w, r)
	if !ok {
		return
	}
	if err := s.categories.DeleteItem(r.Context(), *p.item); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
