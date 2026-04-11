package server

import (
	"github.com/go-chi/chi/v5"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/user"
	authmw "github.com/open-help-desk/open-help-desk/backend/internal/middleware"
)

func (s *Server) authRouter() *chi.Mux {
	r := chi.NewRouter()

	r.Post("/local/login", s.handleLocalLogin)
	r.Post("/local/logout", s.handleLogout)
	r.Post("/local/mfa/verify", s.handleMFAVerify)

	r.Post("/oauth/token", s.handleOAuthToken)

	// SAML routes are mounted dynamically only when SAML is configured.
	if s.samlConfigured() {
		r.Get("/saml/login", s.handleSAMLLogin)
		r.Post("/saml/acs", s.handleSAMLACS)
		r.Get("/saml/metadata", s.handleSAMLMetadata)
	}

	return r
}

func (s *Server) ticketRouter() *chi.Mux {
	r := chi.NewRouter()
	r.Use(authmw.RequireRole(user.RoleAdmin, user.RoleStaff, user.RoleUser))

	r.Post("/", s.handleCreateTicket)
	r.Get("/{id}", s.handleGetTicket)
	r.Patch("/{id}", s.handleUpdateTicket)
	r.Post("/{id}/replies", s.handleAddReply)
	r.Get("/{id}/replies", s.handleListReplies)
	r.Post("/{id}/resolve", s.handleResolveTicket)
	r.Post("/{id}/reopen", s.handleReopenTicket)
	r.Post("/{id}/links", s.handleAddLink)
	r.Delete("/{id}/links/{targetId}/{linkType}", s.handleRemoveLink)
	r.Get("/{id}/links", s.handleListLinks)

	return r
}

func (s *Server) adminRouter() *chi.Mux {
	r := chi.NewRouter()
	r.Use(authmw.RequireRole(user.RoleAdmin))

	r.Route("/users", func(r chi.Router) {
		r.Get("/", s.handleListUsers)
		r.Post("/", s.handleCreateUser)
		r.Get("/{id}", s.handleGetUser)
		r.Patch("/{id}", s.handleUpdateUser)
		r.Delete("/{id}", s.handleDeleteUser)
	})

	r.Route("/groups", func(r chi.Router) {
		r.Get("/", s.handleListGroups)
		r.Post("/", s.handleCreateGroup)
		r.Get("/{id}", s.handleGetGroup)
		r.Patch("/{id}", s.handleUpdateGroup)
		r.Delete("/{id}", s.handleDeleteGroup)
		r.Post("/{id}/members", s.handleAddGroupMember)
		r.Delete("/{id}/members/{userId}", s.handleRemoveGroupMember)
		r.Post("/{id}/scopes", s.handleAddGroupScope)
		r.Delete("/{id}/scopes", s.handleRemoveGroupScope)
	})

	r.Route("/categories", func(r chi.Router) {
		r.Get("/", s.handleListCategories)
		r.Post("/", s.handleCreateCategory)
		r.Get("/{id}", s.handleGetCategory)
		r.Patch("/{id}", s.handleUpdateCategory)
		r.Delete("/{id}", s.handleDeleteCategory)

		r.Get("/{id}/types", s.handleListTypes)
		r.Post("/{id}/types", s.handleCreateType)
		r.Patch("/{id}/types/{typeId}", s.handleUpdateType)
		r.Delete("/{id}/types/{typeId}", s.handleDeleteType)

		r.Get("/{id}/types/{typeId}/items", s.handleListItems)
		r.Post("/{id}/types/{typeId}/items", s.handleCreateItem)
		r.Patch("/{id}/types/{typeId}/items/{itemId}", s.handleUpdateItem)
		r.Delete("/{id}/types/{typeId}/items/{itemId}", s.handleDeleteItem)
	})

	r.Route("/statuses", func(r chi.Router) {
		r.Get("/", s.handleListStatuses)
		r.Post("/", s.handleCreateStatus)
		r.Patch("/{id}", s.handleUpdateStatus)
		r.Delete("/{id}", s.handleDeleteStatus)
	})

	r.Route("/settings", func(r chi.Router) {
		r.Get("/", s.handleGetSettings)
		r.Patch("/", s.handleUpdateSettings)
	})

	r.Route("/plugins", func(r chi.Router) {
		r.Get("/", s.handleListPlugins)
		r.Post("/", s.handleInstallPlugin)
		r.Patch("/{id}", s.handleUpdatePlugin)
		r.Delete("/{id}", s.handleUninstallPlugin)
	})

	r.Route("/api-keys", func(r chi.Router) {
		r.Get("/", s.handleListAPIKeys)
		r.Post("/", s.handleCreateAPIKey)
		r.Delete("/{id}", s.handleDeleteAPIKey)
	})

	r.Route("/oauth-clients", func(r chi.Router) {
		r.Get("/", s.handleListOAuthClients)
		r.Post("/", s.handleCreateOAuthClient)
		r.Delete("/{id}", s.handleDeleteOAuthClient)
	})

	r.Route("/webhooks", func(r chi.Router) {
		r.Get("/", s.handleListWebhooks)
		r.Post("/", s.handleCreateWebhook)
		r.Patch("/{id}", s.handleUpdateWebhook)
		r.Delete("/{id}", s.handleDeleteWebhook)
	})

	return r
}

func (s *Server) meRouter() *chi.Mux {
	r := chi.NewRouter()
	r.Use(authmw.RequireRole(user.RoleAdmin, user.RoleStaff, user.RoleUser))

	r.Get("/", s.handleGetMe)
	r.Patch("/password", s.handleChangePassword)
	r.Get("/mfa/enroll", s.handleMFAEnrollStart)
	r.Post("/mfa/enroll/confirm", s.handleMFAEnrollConfirm)

	return r
}
