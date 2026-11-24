package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// Admin category/type/item management — the three-level CTI taxonomy.
//
// Split out of handler_admin.go, which had grown to 1,332 lines across twelve
// unrelated resources. Moved verbatim: no handler logic changed.

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
	JSON(w, http.StatusOK, existing)
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
	typeID, err := uuid.Parse(chi.URLParam(r, "typeId"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid type ID")
		return
	}
	existing, err := s.categories.GetType(r.Context(), typeID)
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
	JSON(w, http.StatusOK, existing)
}

func (s *Server) handleDeleteType(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "typeId"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid type ID")
		return
	}
	if err := s.categories.DeleteType(r.Context(), id); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListItems(w http.ResponseWriter, r *http.Request) {
	typeID, err := uuid.Parse(chi.URLParam(r, "typeId"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid type ID")
		return
	}
	items, err := s.categories.ListItems(r.Context(), typeID, false)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, items)
}

func (s *Server) handleCreateItem(w http.ResponseWriter, r *http.Request) {
	typeID, err := uuid.Parse(chi.URLParam(r, "typeId"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid type ID")
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
	it, err := s.categories.CreateItem(r.Context(), typeID, body.Name, body.SortOrder)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusCreated, it)
}

func (s *Server) handleUpdateItem(w http.ResponseWriter, r *http.Request) {
	itemID, err := uuid.Parse(chi.URLParam(r, "itemId"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid item ID")
		return
	}
	existing, err := s.categories.GetItem(r.Context(), itemID)
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
	JSON(w, http.StatusOK, existing)
}

func (s *Server) handleDeleteItem(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "itemId"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid item ID")
		return
	}
	if err := s.categories.DeleteItem(r.Context(), id); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
