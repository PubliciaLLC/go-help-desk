package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/open-help-desk/open-help-desk/backend/internal/database/authstore"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/auth"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/category"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/group"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/ticket"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/user"
	authmw "github.com/open-help-desk/open-help-desk/backend/internal/middleware"
)

// ── Users ────────────────────────────────────────────────────────────────��───

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.users.List(r.Context(), 100, 0)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, users)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
		Password    string `json:"password"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	u, err := s.users.Create(r.Context(), user.CreateUserInput{
		Email:       body.Email,
		DisplayName: body.DisplayName,
		Role:        user.Role(body.Role),
		Password:    body.Password,
	})
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusCreated, u)
}

func (s *Server) handleGetUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid user ID")
		return
	}
	u, err := s.users.GetByID(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, u)
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid user ID")
		return
	}
	u, err := s.users.GetByID(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	var body struct {
		DisplayName *string `json:"display_name"`
		Role        *string `json:"role"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if body.DisplayName != nil {
		u.DisplayName = *body.DisplayName
	}
	if body.Role != nil {
		u.Role = user.Role(*body.Role)
	}
	if err := s.users.Update(r.Context(), u); err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, u)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid user ID")
		return
	}
	if err := s.users.SoftDelete(r.Context(), id); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

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
	JSON(w, http.StatusOK, existing)
}

func (s *Server) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid group ID")
		return
	}
	if err := s.groups.Delete(r.Context(), id); err != nil {
		handleError(w, err)
		return
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
	if err := s.groups.AddMember(r.Context(), groupID, body.UserID); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

// ── Statuses ─────────────────────────────────────────────────────────────────

func (s *Server) handleListStatuses(w http.ResponseWriter, r *http.Request) {
	statuses, err := s.tickets.ListStatuses(r.Context())
	if err != nil {
		handleError(w, err)
		return
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

// ── Settings ─────────────────────────────────────────────────────────────────

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	all, err := s.adminSvc.ListAll(r.Context())
	if err != nil {
		handleError(w, err)
		return
	}
	// Convert raw bytes to JSON-parseable map.
	out := make(map[string]json.RawMessage, len(all))
	for k, v := range all {
		out[k] = json.RawMessage(v)
	}
	JSON(w, http.StatusOK, out)
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]json.RawMessage
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	for k, v := range body {
		if err := s.adminSvc.SetRaw(r.Context(), k, []byte(v)); err != nil {
			handleError(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Plugins ──────────────────────────────────────────────────────────────────

func (s *Server) handleListPlugins(w http.ResponseWriter, r *http.Request) {
	JSON(w, http.StatusOK, s.plugins.List())
}

func (s *Server) handleInstallPlugin(w http.ResponseWriter, r *http.Request) {
	Error(w, http.StatusNotImplemented, "not_implemented", "WASM plugin upload not yet implemented")
}

func (s *Server) handleUpdatePlugin(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	var err error
	if body.Enabled {
		err = s.plugins.Enable(id)
	} else {
		err = s.plugins.Disable(id)
	}
	if err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUninstallPlugin(w http.ResponseWriter, r *http.Request) {
	Error(w, http.StatusNotImplemented, "not_implemented", "plugin uninstall not yet implemented")
}

// ── API Keys ─────────────────────────────────────────────────────────────────

func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	keys, err := s.authStore.ListByUser(r.Context(), a.UserID)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, keys)
}

func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	var body struct {
		Name      string   `json:"name"`
		Scopes    []string `json:"scopes"`
		ExpiresAt *string  `json:"expires_at"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	raw, hashed, err := auth.GenerateToken()
	if err != nil {
		handleError(w, err)
		return
	}
	key := auth.APIKey{
		ID:          uuid.New(),
		Name:        body.Name,
		HashedToken: hashed,
		UserID:      a.UserID,
		Scopes:      body.Scopes,
		CreatedAt:   time.Now(),
	}
	if err := s.authStore.CreateAPIKey(r.Context(), key); err != nil {
		handleError(w, err)
		return
	}
	// Return the raw token once — it will never be shown again.
	JSON(w, http.StatusCreated, map[string]any{
		"id":    key.ID,
		"token": raw, // shown once
		"name":  key.Name,
	})
}

func (s *Server) handleDeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ID")
		return
	}
	if err := s.authStore.Delete(r.Context(), id); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── OAuth Clients ────────────────────────────────────────────────────────────

func (s *Server) handleListOAuthClients(w http.ResponseWriter, r *http.Request) {
	clients, err := s.authStore.ListOAuthClients(r.Context())
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, clients)
}

func (s *Server) handleCreateOAuthClient(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	raw, hashed, err := auth.GenerateToken()
	if err != nil {
		handleError(w, err)
		return
	}
	clientIDRaw, _, err2 := auth.GenerateToken()
	if err2 != nil {
		handleError(w, err2)
		return
	}
	client := auth.OAuthClient{
		ID:           uuid.New(),
		ClientID:     clientIDRaw[:16],
		HashedSecret: hashed,
		Name:         body.Name,
		Scopes:       body.Scopes,
		CreatedAt:    time.Now(),
	}
	if err := s.authStore.CreateOAuthClient(r.Context(), client); err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusCreated, map[string]any{
		"client_id":     client.ClientID,
		"client_secret": raw,
		"name":          client.Name,
	})
}

func (s *Server) handleDeleteOAuthClient(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ID")
		return
	}
	if err := s.authStore.DeleteOAuthClient(r.Context(), id); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Webhooks ─────────────────────────────────────────────────────────────────

func (s *Server) handleListWebhooks(w http.ResponseWriter, r *http.Request) {
	webhooks, err := s.authStore.ListEnabledWebhooks(r.Context())
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, webhooks)
}

func (s *Server) handleCreateWebhook(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL    string   `json:"url"`
		Events []string `json:"events"`
		Secret string   `json:"secret"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	wh := authstore.WebhookConfig{
		ID:        uuid.New(),
		URL:       body.URL,
		Events:    body.Events,
		Secret:    body.Secret,
		Enabled:   true,
		CreatedAt: time.Now(),
	}
	if err := s.authStore.CreateWebhook(r.Context(), wh); err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusCreated, wh)
}

func (s *Server) handleUpdateWebhook(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ID")
		return
	}
	existing, err := s.authStore.GetWebhook(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	var body struct {
		URL     *string  `json:"url"`
		Events  []string `json:"events"`
		Secret  *string  `json:"secret"`
		Enabled *bool    `json:"enabled"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if body.URL != nil {
		existing.URL = *body.URL
	}
	if body.Events != nil {
		existing.Events = body.Events
	}
	if body.Secret != nil {
		existing.Secret = *body.Secret
	}
	if body.Enabled != nil {
		existing.Enabled = *body.Enabled
	}
	if err := s.authStore.UpdateWebhook(r.Context(), existing); err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, existing)
}

func (s *Server) handleDeleteWebhook(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ID")
		return
	}
	if err := s.authStore.DeleteWebhook(r.Context(), id); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Ensure category types are imported (used in handler bodies).
var _ category.Category
var _ group.GroupScope
var _ ticket.Status
