// Package server implements the HTTP layer. It is a pure translation layer:
// parse request → call service → write response. No business logic lives here.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
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
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/customfield"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/group"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/plugin"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/tag"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/ticket"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/user"
	authmw "github.com/open-help-desk/open-help-desk/backend/internal/middleware"
	"github.com/open-help-desk/open-help-desk/backend/internal/version"
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

	users        *user.Service
	tickets      *ticket.Service
	categories   *category.Service
	groups       *group.Service
	tags         *tag.Service
	adminSvc     *admin.Service
	customFields *customfield.Service
	plugins      plugin.Registry

	apiKeyLookup     authmw.APIKeyAuthFunc
	oauthClientStore OAuthClientLookup
	authStore        AuthStoreIface

	// samlMu guards samlHandler. The handler is nil when SAML is not configured.
	samlMu      sync.RWMutex
	samlHandler http.Handler
}

// New constructs a Server and registers all routes.
func New(
	cfg *config.Config,
	sessionStore sessions.Store,
	users *user.Service,
	tickets *ticket.Service,
	categories *category.Service,
	groups *group.Service,
	tags *tag.Service,
	adminSvc *admin.Service,
	customFields *customfield.Service,
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
		tags:             tags,
		adminSvc:         adminSvc,
		customFields:     customFields,
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

// statusRecorder wraps ResponseWriter to capture the written status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

// requestLogger logs each request at INFO level once it completes.
// It includes whether the session cookie was present on the request and
// whether a Set-Cookie header was written on the response, to make
// session/auth issues diagnosable without needing debug mode.
func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, cookieErr := r.Cookie(auth.SessionName)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)
		setCookie := rec.Header().Get("Set-Cookie")
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"ms", time.Since(start).Milliseconds(),
			"session_cookie_in", cookieErr == nil,
			"session_cookie_out", setCookie != "",
		)
		if setCookie != "" {
			slog.Debug("set-cookie header", "value", setCookie)
		}
	})
}

func (s *Server) buildRouter() *chi.Mux {
	r := chi.NewRouter()
	r.Use(chimw.RealIP)
	r.Use(chimw.RequestID)
	r.Use(chimw.Recoverer)
	r.Use(requestLogger)

	// Auth middleware chain: each layer runs only when no prior actor is set.
	r.Use(authmw.SessionAuth(s.sessions))
	r.Use(authmw.APIKeyAuth(s.apiKeyLookup))
	r.Use(authmw.BearerAuth(s.cfg.JWTSecret))

	// Health check — no auth required.
	r.Get("/health", s.handleHealth)

	r.Route("/api/v1", func(r chi.Router) {
		// Public endpoints — no auth required.
		r.Get("/site", s.handleGetSiteConfig)
		r.Get("/logo", s.handleServeLogo)
		r.Get("/setup/status", s.handleSetupStatus)
		r.Post("/setup", s.handleSetup)

		r.Mount("/auth", s.authRouter())
		r.Mount("/tickets", s.ticketRouter())
		r.Mount("/groups", s.groupsRouter())
		r.With(authmw.RequireRole(user.RoleAdmin, user.RoleStaff, user.RoleUser)).Get("/tags", s.handleListActiveTags)
		// Public category/type listing for ticket creation (active only, no admin required).
		r.Get("/categories", s.handleListPublicCategories)
		r.Get("/categories/{id}/types", s.handleListPublicTypes)
		r.Mount("/admin", s.adminRouter())
		r.Mount("/me", s.meRouter())
	})

	return r
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleListPublicCategories returns only active categories.
// Used by the ticket-creation form for regular users and guests.
// No admin auth required — any authenticated user or guest can call this.
func (s *Server) handleListPublicCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := s.categories.ListCategories(r.Context(), true) // active only
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, cats)
}

// handleListPublicTypes returns only active types for a category.
func (s *Server) handleListPublicTypes(w http.ResponseWriter, r *http.Request) {
	catID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid_id", "invalid category id")
		return
	}
	types, err := s.categories.ListTypes(r.Context(), catID, true) // active only
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, types)
}

// handleGetSiteConfig returns public-facing branding info and the app version.
// No authentication required — used by the SPA shell before login.
func (s *Server) handleGetSiteConfig(w http.ResponseWriter, r *http.Request) {
	JSON(w, http.StatusOK, map[string]string{
		"name":     s.adminSvc.SiteName(r.Context()),
		"logo_url": s.adminSvc.SiteLogoURL(r.Context()),
		"version":  version.Version,
	})
}

// InitSAML reads SAML config from the database and initialises the SP
// middleware if all three fields (cert, key, metadata URL) are present.
// It is called once at startup; a non-fatal error is logged and ignored so
// that the server starts even when SAML is not yet configured.
func (s *Server) InitSAML(ctx context.Context) {
	if err := s.reloadSAML(ctx); err != nil {
		slog.Warn("SAML middleware not loaded at startup", "error", err)
	}
}

// reloadSAML reads the three SAML settings from the database, (re)initialises
// the crewjam/saml middleware, and stores it for use by the request handlers.
// Callers must hold no lock; this method acquires the write lock internally.
func (s *Server) reloadSAML(ctx context.Context) error {
	metadataURL, certPEM, keyPEM := s.adminSvc.GetSAMLConfig(ctx)
	if metadataURL == "" || certPEM == "" || keyPEM == "" {
		// Not yet configured — clear any previously loaded handler.
		s.samlMu.Lock()
		s.samlHandler = nil
		s.samlMu.Unlock()
		return nil
	}

	mw, err := auth.NewSAMLMiddleware(auth.SAMLConfig{
		BaseURL:     s.cfg.BaseURL,
		MetadataURL: metadataURL,
		CertPEM:     []byte(certPEM),
		KeyPEM:      []byte(keyPEM),
	})
	if err != nil {
		return err
	}

	s.samlMu.Lock()
	s.samlHandler = mw
	s.samlMu.Unlock()
	slog.Info("SAML middleware loaded", "metadata_url", metadataURL)
	return nil
}

// samlHTTP returns the current SAML handler under a read lock, or nil.
func (s *Server) samlHTTP() http.Handler {
	s.samlMu.RLock()
	defer s.samlMu.RUnlock()
	return s.samlHandler
}

// Prevent unused import errors.
var (
	_ = time.Now
	_ = uuid.Nil
)
