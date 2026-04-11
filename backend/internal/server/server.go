// Package server implements the HTTP layer. It is a pure translation layer:
// parse request → call service → write response. No business logic lives here.
package server

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/gorilla/sessions"
	"github.com/open-help-desk/open-help-desk/backend/internal/config"
	"github.com/open-help-desk/open-help-desk/backend/internal/database/authstore"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/admin"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/auth"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/category"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/group"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/plugin"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/ticket"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/user"
	authmw "github.com/open-help-desk/open-help-desk/backend/internal/middleware"
)

// OAuthClientLookup fetches an OAuth client by client ID.
type OAuthClientLookup interface {
	GetByClientID(ctx context.Context, clientID string) (auth.OAuthClient, error)
}

// AuthStoreIface is the server-facing interface for auth key management.
type AuthStoreIface interface {
	CreateAPIKey(ctx context.Context, k auth.APIKey) error
	Delete(ctx context.Context, id uuid.UUID) error
	ListByUser(ctx context.Context, userID uuid.UUID) ([]auth.APIKey, error)
	CreateOAuthClient(ctx context.Context, c auth.OAuthClient) error
	DeleteOAuthClient(ctx context.Context, id uuid.UUID) error
	ListOAuthClients(ctx context.Context) ([]auth.OAuthClient, error)
	CreateWebhook(ctx context.Context, wh authstore.WebhookConfig) error
	GetWebhook(ctx context.Context, id uuid.UUID) (authstore.WebhookConfig, error)
	UpdateWebhook(ctx context.Context, wh authstore.WebhookConfig) error
	DeleteWebhook(ctx context.Context, id uuid.UUID) error
	ListEnabledWebhooks(ctx context.Context) ([]authstore.WebhookConfig, error)
}

// Server is the top-level HTTP handler.
type Server struct {
	cfg      *config.Config
	router   *chi.Mux
	sessions sessions.Store

	users      *user.Service
	tickets    *ticket.Service
	categories *category.Service
	groups     *group.Service
	adminSvc   *admin.Service
	plugins    plugin.Registry

	apiKeyLookup     authmw.APIKeyAuthFunc
	oauthClientStore OAuthClientLookup
	authStore        AuthStoreIface
}

// New constructs a Server and registers all routes.
func New(
	cfg *config.Config,
	sessionStore sessions.Store,
	users *user.Service,
	tickets *ticket.Service,
	categories *category.Service,
	groups *group.Service,
	adminSvc *admin.Service,
	plugins plugin.Registry,
	apiKeyLookup authmw.APIKeyAuthFunc,
	oauthClients OAuthClientLookup,
	authStore AuthStoreIface,
) *Server {
	s := &Server{
		cfg:              cfg,
		sessions:         sessionStore,
		users:            users,
		tickets:          tickets,
		categories:       categories,
		groups:           groups,
		adminSvc:         adminSvc,
		plugins:          plugins,
		apiKeyLookup:     apiKeyLookup,
		oauthClientStore: oauthClients,
		authStore:        authStore,
	}
	s.router = s.buildRouter()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

func (s *Server) buildRouter() *chi.Mux {
	r := chi.NewRouter()
	r.Use(chimw.RealIP)
	r.Use(chimw.RequestID)
	r.Use(chimw.Recoverer)

	// Auth middleware chain: each layer runs only when no prior actor is set.
	r.Use(authmw.SessionAuth(s.sessions))
	r.Use(authmw.APIKeyAuth(s.apiKeyLookup))
	r.Use(authmw.BearerAuth(s.cfg.JWTSecret))

	// Health check — no auth required.
	r.Get("/health", s.handleHealth)

	r.Route("/api/v1", func(r chi.Router) {
		r.Mount("/auth", s.authRouter())
		r.Mount("/tickets", s.ticketRouter())
		r.Mount("/admin", s.adminRouter())
		r.Mount("/me", s.meRouter())
	})

	return r
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// samlConfigured returns true when the SAML cert/key/metadata are all set.
func (s *Server) samlConfigured() bool {
	return s.cfg.SAMLEnabled &&
		s.cfg.SAMLCertFile != "" &&
		s.cfg.SAMLKeyFile != "" &&
		s.cfg.SAMLMetadataURL != ""
}

// newSAMLMiddleware initialises the SAML SP middleware on demand.
func (s *Server) newSAMLMiddleware() (http.Handler, error) {
	return auth.NewSAMLMiddleware(auth.SAMLConfig{
		BaseURL:     s.cfg.BaseURL,
		MetadataURL: s.cfg.SAMLMetadataURL,
		CertFile:    s.cfg.SAMLCertFile,
		KeyFile:     s.cfg.SAMLKeyFile,
	})
}

// Prevent unused import errors.
var (
	_ = time.Now
	_ = uuid.Nil
)
