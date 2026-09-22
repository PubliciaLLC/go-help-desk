// Package server implements the HTTP layer. It is a pure translation layer:
// parse request → call service → write response. No business logic lives here.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/crewjam/saml/samlsp"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/gorilla/sessions"
	"github.com/publiciallc/go-help-desk/backend/internal/config"
	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/cannedresponse"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/plugin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/registration"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/tag"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
	"github.com/publiciallc/go-help-desk/backend/internal/version"
)

// Server holds its domain services as concrete types, not interfaces, and does
// so deliberately.
//
// Inverting them was raised as an architecture finding (#73) on the grounds
// that the HTTP tests cannot run without Postgres. That is true, but the cost
// and the benefit were both measured before deciding:
//
//   - The handlers call 140 distinct methods across the eleven services, so
//     the change is ~140 interface declarations plus ~140 fake methods to get
//     a database-free harness.
//   - The pain it was meant to relieve is gone. The suite took 209s in CI
//     when the finding was written; it takes ~15s now, and the cause was
//     bcrypt at production cost (#68), not the coupling. Locally it is 5s
//     against an ephemeral container that takes 15s to start (#67).
//   - CLAUDE.md is explicit: "Do not mock the DB. Mocks hide the bugs that
//     matter most." These tests earn that rule — they have caught a missing
//     claim producing a 500, unique-index behaviour, and foreign-key
//     violations that service-level fakes would have sailed past.
//
// The three fields below that ARE interfaces — OAuthClientLookup,
// AuthStoreIface, APIKeyAuthFunc — exist for a different reason: they are
// narrow contracts consumed across a package boundary, not seams introduced
// for testing. That is the distinction, and it is why eleven concrete services
// sitting beside three interfaces is not an unfinished refactor.
//
// Revisit if the services grow behaviour worth testing at the HTTP layer
// without a database, or if the integration suite becomes slow again.

// ProtectMCP wraps an MCP handler in exactly the middleware chain that guards
// /api/, so every MCP call is authenticated.
//
// The MCP handler is mounted on the root ServeMux beside /api/ rather than
// inside this router, so it never passed through the chain below. It was
// reachable with no credentials at all: an unauthenticated caller could read
// any ticket by tracking number and post replies while naming any user as the
// author. The package comment claimed it "uses the same auth methods (API key,
// bearer token) as the REST API", which is presumably why nobody checked.
//
// It authenticates; it does not authorise. Every signed-in role is admitted,
// including RoleUser, and what a caller may then DO is decided per tool in
// internal/mcp: write tools require staff, and reads are filtered through the
// Authorizer this Server implements.
//
// That split is deliberate. This surface was originally narrowed to staff and
// admin because duplicating the REST API's per-handler scoping here is how the
// two drifted apart in the first place. Nothing is duplicated now — internal/mcp
// consumes CanViewTicket and TicketVisibility rather than reimplementing them,
// so there is one rule with two callers instead of two rules.
//
// RequireMFA is included for parity with ticketRouter — MCP must not be a way
// to bypass a TOTP challenge. Machine credentials are unaffected: the API-key
// and bearer paths set MFAPassed on the actor they attach.
func (s *Server) ProtectMCP(next http.Handler) http.Handler {
	// Order matches ticketRouter's r.Use sequence: RequireRole before
	// RequireMFA. Reversed, a caller with no credentials at all is told
	// "MFA verification required" (403) instead of "authentication required"
	// (401), which is both wrong and a confusing thing to debug.
	// Reporting users are admitted so they can read their own tickets. The
	// transport no longer decides what a caller may do — every tool gates its
	// own writes on requireStaff and filters its own reads through the
	// Authorizer. Widening here without those checks would re-open
	// GHSA-2x4f-j4jv-m2cm to a lesser degree.
	chain := authmw.RequireRole(user.RoleAdmin, user.RoleStaff, user.RoleUser)(
		authmw.RequireMFA(next),
	)
	chain = authmw.BearerAuth(s.cfg.JWTSecret, s.oauthClientStore.GetByClientID)(chain)
	chain = authmw.APIKeyAuth(s.apiKeyLookup)(chain)
	chain = authmw.SessionAuth(s.sessions)(chain)

	// /mcp/ is mounted on the bare ServeMux, not on the chi router, so it never
	// reaches the r.Use stack that gives every /api/ request an id, a log line,
	// and a panic guard. GHSA-5g72-m483-3v63 named that gap alongside the auth
	// one ("bypasses the entire chi middleware chain, auth and logging alike");
	// the auth half was fixed and this half was not, so MCP traffic left no
	// request-log trace at all. GHSA-2x4f-j4jv-m2cm tells operators to review
	// recent activity for signs of exploitation — against a tool surface that
	// was not logging, that instruction could not be carried out.
	//
	// Outermost, so the log line records the status the client actually saw,
	// including the 401s and 403s the auth chain produces.
	return chimw.RequestID(chimw.Recoverer(requestLogger(chain)))
}

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
	// loginLimiter throttles password and signup attempts, keyed on the
	// submitted email and the transport address respectively.
	//
	// TOTP attempts are NOT limited here: they are counted on the user row
	// (user.RecordMFAFailure), because an in-memory counter is cleared by a
	// restart and multiplied by the replica count, which is not a limit on a
	// six-digit secret.
	loginLimiter *authmw.RateLimiter

	// guestResendLimiter is per ticket, and much tighter than the credential
	// budget: a resend rotates, so anyone who can guess a sequential tracking
	// number and knows the address could otherwise replace the link a customer
	// is holding ten times a minute, indefinitely, from one address. One every
	// five minutes still lets a customer who lost their link get another
	// straight away, and turns a sustained lockout into an inconvenience.
	guestResendLimiter *authmw.RateLimiter

	cfg      *config.Config
	router   *chi.Mux
	sessions SessionStore

	users           *user.Service
	tickets         *ticket.Service
	registration    *registration.Service
	categories      *category.Service
	groups          *group.Service
	tags            *tag.Service
	adminSvc        *admin.Service
	customFields    *customfield.Service
	slaPolicies     *sla.Service
	plugins         plugin.Registry
	cannedResponses *cannedresponse.Service

	apiKeyLookup     authmw.APIKeyAuthFunc
	oauthClientStore OAuthClientLookup
	authStore        AuthStoreIface

	// samlMu guards samlHandler. The handler is nil when SAML is not configured.
	samlMu      sync.RWMutex
	samlHandler *samlsp.Middleware

	// oidcMu guards oidcProvider because OIDC configuration can be
	// reloaded at runtime from the admin settings UI.
	oidcMu sync.RWMutex

	// oidcProvider contains the configured OpenID Connect provider.
	oidcProvider *auth.OIDCProvider

	// rrIdx is the round-robin counter for auto-assigning tickets to users.
	rrIdx atomic.Uint64
}

// New constructs a Server and registers all routes.
func New(
	cfg *config.Config,
	sessionStore SessionStore,
	users *user.Service,
	tickets *ticket.Service,
	categories *category.Service,
	groups *group.Service,
	tags *tag.Service,
	adminSvc *admin.Service,
	customFields *customfield.Service,
	slaPolicies *sla.Service,
	plugins plugin.Registry,
	apiKeyLookup authmw.APIKeyAuthFunc,
	oauthClients OAuthClientLookup,
	authStore AuthStoreIface,
	registrationSvc *registration.Service,
	cannedResponses *cannedresponse.Service,
) *Server {
	s := &Server{
		cfg:              cfg,
		sessions:         sessionStore,
		users:            users,
		tickets:          tickets,
		registration:     registrationSvc,
		categories:       categories,
		groups:           groups,
		tags:             tags,
		adminSvc:         adminSvc,
		customFields:     customFields,
		slaPolicies:      slaPolicies,
		plugins:          plugins,
		apiKeyLookup:     apiKeyLookup,
		oauthClientStore: oauthClients,
		authStore:        authStore,
		cannedResponses:  cannedResponses,
		// Built here rather than injected: derived entirely from config, no
		// other collaborators.
		loginLimiter:       authmw.NewRateLimiter(cfg.AuthRateLimitPerMinute, time.Minute),
		guestResendLimiter: authmw.NewRateLimiter(1, 5*time.Minute),
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

// Flush forwards to the underlying writer. Embedding http.ResponseWriter gives
// this type that interface and nothing else, so without an explicit forward a
// `w.(http.Flusher)` assertion downstream fails even when the real writer
// flushes fine. mcp-go's SSE transport makes exactly that assertion and answers
// 500 "Streaming unsupported" when it misses, which killed MCP entirely the
// moment this logger was placed in front of it.
func (sr *statusRecorder) Flush() {
	if f, ok := sr.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the writer underneath, which is the
// modern way past this whole class of wrapper problem — deadlines, and any
// optional interface added to net/http later.
func (sr *statusRecorder) Unwrap() http.ResponseWriter { return sr.ResponseWriter }

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

// SessionStore is the session contract this package needs: gorilla's Store for
// the ordinary read/write path, plus revocation.
//
// Revocation is the reason sessions moved server-side, so it belongs in the
// interface rather than behind a type assertion at each call site — a handler
// that forgets to revoke is the failure mode, and an interface method is
// harder to forget than an assertion.
type SessionStore interface {
	sessions.Store

	// DeleteForUser revokes every session a user holds, for the events after
	// which an existing session is wrong: disable, role change, password
	// change, MFA reset.
	DeleteForUser(ctx context.Context, userID uuid.UUID) error

	// Delete removes one session, which is how the id is rotated when a
	// session gains authority.
	Delete(ctx context.Context, id string) error
}

func (s *Server) buildRouter() *chi.Mux {
	r := chi.NewRouter()
	// chimw.RealIP is deliberately NOT installed. It overwrites RemoteAddr
	// from X-Forwarded-For, which is attacker controlled; its only consumer
	// here was the rate limiter, and it made that limiter bypassable with a
	// header. Nothing else reads RemoteAddr. If real client addresses are
	// wanted later — a login audit trail is the plausible reason — they come
	// back with a trusted-proxy setting attached, so that getting it wrong
	// costs a log field rather than a security control.
	r.Use(chimw.RequestID)
	r.Use(chimw.Recoverer)
	r.Use(requestLogger)
	r.Use(securityHeaders)

	// Auth middleware chain: each layer runs only when no prior actor is set.
	r.Use(authmw.SessionAuth(s.sessions))
	r.Use(authmw.APIKeyAuth(s.apiKeyLookup))
	r.Use(authmw.BearerAuth(s.cfg.JWTSecret, s.oauthClientStore.GetByClientID))

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
		r.Mount("/guest", s.guestRouter())
		r.Mount("/groups", s.groupsRouter())
		// RequireMFA as well as RequireRole: without it a session that has
		// passed the password but not the second factor could still read
		// these. Small in isolation, but a half-authenticated session should
		// reach nothing but the challenge it still owes.
		r.With(authmw.RequireRole(user.RoleAdmin, user.RoleStaff, user.RoleUser), authmw.RequireMFA).
			With(authmw.RequireResource(auth.ResourceTickets),
				authmw.RequireRole(user.RoleAdmin, user.RoleStaff)).
			Get("/tags", s.handleListActiveTags)
		// Public category/type/item listing (active only, no admin required).
		r.Get("/categories", s.handleListPublicCategories)
		r.Get("/categories/{id}/types", s.handleListPublicTypes)
		r.Get("/categories/{id}/types/{typeId}/items", s.handleListPublicItems)
		// Statuses are needed by all authenticated users for display (ticket list, detail, dashboard).
		r.With(authmw.RequireRole(user.RoleAdmin, user.RoleStaff, user.RoleUser), authmw.RequireMFA).
			With(authmw.RequireResource(auth.ResourceTickets)).Get("/statuses", s.handleListStatuses)
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

// handleListPublicItems returns only active items for a type.
func (s *Server) handleListPublicItems(w http.ResponseWriter, r *http.Request) {
	typeID, err := uuid.Parse(chi.URLParam(r, "typeId"))
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid_id", "invalid type id")
		return
	}
	items, err := s.categories.ListItems(r.Context(), typeID, true) // active only
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, items)
}

// handleGetSiteConfig returns public-facing branding info and the app version.
// No authentication required — used by the SPA shell before login.
func (s *Server) handleGetSiteConfig(w http.ResponseWriter, r *http.Request) {
	// guest_submission_enabled is here rather than in a status endpoint of its
	// own because the login page needs it to decide whether to offer a link to
	// /submit, and offering a dead end is worse than offering nothing. It says
	// only whether the instance accepts guest tickets — the same thing an
	// unauthenticated POST to /guest/tickets would reveal by answering 404.
	JSON(w, http.StatusOK, map[string]any{
		"name":                     s.adminSvc.SiteName(r.Context()),
		"logo_url":                 s.adminSvc.SiteLogoURL(r.Context()),
		"version":                  version.Version,
		"guest_submission_enabled": s.adminSvc.GuestSubmissionEnabled(r.Context()),
	})
}

// InitSAML reads SAML config from the database and initialises the SP
// middleware if all three fields (cert, key, metadata URL) are present.
// It is called once at startup; a non-fatal error is logged and ignored so
// that the server starts even when SAML is not yet configured.

// InitOIDC initializes the OpenID Connect provider.
// OIDC is optional; startup continues when it is not configured.
func (s *Server) InitOIDC(ctx context.Context) error {
	cfg := s.adminSvc.GetOIDCConfig(ctx)

	// OIDC is explicitly disabled. Clear the active provider.
	if !cfg.Enabled {
		s.oidcMu.Lock()
		s.oidcProvider = nil
		s.oidcMu.Unlock()
		return nil
	}

	// Enabled but incomplete configuration is invalid.
	if cfg.IssuerURL == "" ||
		cfg.ClientID == "" ||
		cfg.ClientSecret == "" {
		err := fmt.Errorf("OIDC configuration is incomplete")
		slog.Warn("OIDC provider not loaded", "error", err)
		return err
	}

	if cfg.RedirectURL == "" {
		cfg.RedirectURL = s.cfg.BaseURL +
			"/api/v1/auth/oidc/callback"
	}

	// Discover and initialize the new provider BEFORE replacing the
	// currently active provider. If discovery fails, leave the existing
	// provider untouched so a bad admin change cannot take OIDC offline.
	provider, err := auth.NewOIDCProvider(ctx, cfg)
	if err != nil {
		slog.Warn("OIDC provider reload failed; keeping existing provider",
			"error", err)
		return fmt.Errorf("OIDC provider initialization failed: %w", err)
	}

	// Swap only after successful initialization.
	s.oidcMu.Lock()
	s.oidcProvider = provider
	s.oidcMu.Unlock()

	slog.Info("OIDC provider loaded", "issuer", cfg.IssuerURL)
	return nil
}

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

	mw, err := auth.NewSAMLMiddleware(ctx, auth.SAMLConfig{
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

// samlHTTP returns the current SAML middleware under a read lock, or nil.
func (s *Server) samlHTTP() *samlsp.Middleware {
	s.samlMu.RLock()
	defer s.samlMu.RUnlock()
	return s.samlHandler
}

// Prevent unused import errors.
var (
	_ = time.Now
	_ = uuid.Nil
)
