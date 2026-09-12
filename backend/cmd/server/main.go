package main

import (
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/sessions"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/publiciallc/go-help-desk/backend/internal/config"
	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/database/adminstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/cannedresponsestore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/customfieldstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/groupstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/registrationstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/slastore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/tagstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/txrunner"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
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
	"github.com/publiciallc/go-help-desk/backend/internal/mcp"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
	"github.com/publiciallc/go-help-desk/backend/internal/server"
	"github.com/publiciallc/go-help-desk/backend/internal/server/notify"
	"github.com/publiciallc/go-help-desk/backend/internal/ui"
)

func main() {
	if err := run(); err != nil {
		// Not log.Fatalf. run() installs a slog JSON handler as the default,
		// which also routes the stdlib log package — and Go's stdlib-log
		// bridge emits at INFO. A container that refuses to start was
		// therefore reporting the reason at level INFO, where log-based
		// alerting does not look for it.
		//
		// slog.Error works on both sides of that setup: before run() has
		// configured a handler it writes text to stderr, afterwards JSON to
		// stdout. Either way the level is right.
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ── Config ────────────────────────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// ── Logger ────────────────────────────────────────────────────────────────
	var logLevel slog.Level
	if err := logLevel.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		// Before SetDefault below, so this goes to slog's default handler
		// (text, stderr) — which is fine, and keeps the level honest.
		slog.Warn("invalid LOG_LEVEL, defaulting to info", "value", cfg.LogLevel)
		logLevel = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})))

	// A copied .env.example starts cleanly and is entirely forgeable: the
	// example SESSION_SECRET is 47 characters, so the 32-character minimum
	// does not catch it. Refusing to boot would be hostile to anyone kicking
	// the tyres, so this is loud instead — at ERROR, repeated, and surfaced to
	// administrators in the UI via /api/v1/admin/security-warnings.
	if insecure := cfg.InsecureSecrets(); len(insecure) > 0 {
		slog.Error("INSECURE CONFIGURATION: example secrets are in use",
			"secrets", insecure,
			"impact", "anyone can forge sessions or API tokens for this instance",
			"fix", "generate real values with: openssl rand -base64 32")
	}

	// ── Database ─────────────────────────────────────────────────────────────
	// Run migrations before opening the pool so the schema is always current.
	if err := database.Migrate(ctx, database.MigrateURL(cfg.DatabaseURL)); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}

	pool, err := database.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer pool.Close()

	// sqlc requires a database/sql-compatible interface; wrap the pgxpool.
	sqlDB := stdlib.OpenDBFromPool(pool)
	q := dbgen.New(sqlDB)

	// ── Stores ────────────────────────────────────────────────────────────────
	uStore := userstore.New(q)
	tStore := ticketstore.New(q)
	cStore := categorystore.New(q)
	gStore := groupstore.New(q)
	aStore := adminstore.New(q)
	auStore := auditstore.New(q)
	slStore := slastore.New(q)
	authStore := authstore.New(q)
	cfStore := customfieldstore.New(q)
	regStore := registrationstore.New(q)
	crStore := cannedresponsestore.New(q)

	// ── Domain services ───────────────────────────────────────────────────────
	tagStore := tagstore.New(q)

	userSvc := user.NewService(uStore)
	categorySvc := category.NewService(cStore)
	groupSvc := group.NewService(gStore)
	tagSvc := tag.NewService(tagStore)
	adminSvc := admin.NewService(aStore)
	customFieldSvc := customfield.NewService(cfStore)
	cannedResponseSvc := cannedresponse.NewService(crStore)

	// slaPolicySvc is always created so the admin blade can manage policies
	// regardless of whether SLA enforcement is active.
	slaPolicySvc := sla.NewService(slStore)

	// slaSvc is passed to the ticket service for enforcement; nil when disabled
	// via the SLA_ENABLED env var (preserves existing behaviour).
	// Declared as the interface type so the zero value is a true nil interface,
	// not a (*sla.Service)(nil) wrapped in an interface (which would pass != nil checks).
	var slaSvc ticket.SLAService
	if cfg.SLAEnabled {
		slaSvc = sla.NewService(slStore)
	}

	// ── Notifications ─────────────────────────────────────────────────────────
	emailDisp, err := notify.NewEmailDispatcher(cfg)
	if err != nil {
		return fmt.Errorf("initialising email dispatcher: %w", err)
	}
	webhookDisp := notify.NewWebhookDispatcher(authStore)
	dispatcher := notify.NewMulti(emailDisp, webhookDisp)

	// ── Registration service ──────────────────────────────────────────────────
	registrationSvc := registration.NewService(regStore, userSvc, emailDisp, cfg.BaseURL)

	// ── Plugin registry ───────────────────────────────────────────────────────
	pluginRegistry := plugin.NewRegistry()

	// ── Ticket service ────────────────────────────────────────────────────────
	// txRunner groups each composite ticket write — the ticket row, its status
	// history, its audit entry — into a single transaction.
	txRunner := txrunner.New(sqlDB)
	ticketSvc := ticket.NewService(tStore, tStore, dispatcher, auStore, txRunner, slaSvc)
	if err := ticketSvc.LoadSystemStatuses(ctx); err != nil {
		return fmt.Errorf("loading system statuses: %w", err)
	}

	// ── Auth helpers ──────────────────────────────────────────────────────────
	// Session cookies carry SessionData as gob, so the concrete type must be
	// registered before any session is written or read. Done here, in the
	// wiring, rather than in a package init() — CLAUDE.md forbids init() with
	// side effects, and a global registry mutated at import time is exactly
	// the kind of action-at-a-distance that rule exists to prevent.
	gob.Register(auth.SessionData{})
	// Two derived keys, not one raw secret: the second encrypts the cookie, so
	// session contents are no longer readable by whoever holds it.
	sessionHashKey, sessionBlockKey, err := auth.DeriveSessionKeys(cfg.SessionSecret)
	if err != nil {
		return fmt.Errorf("deriving session keys: %w", err)
	}
	sessionStore := sessions.NewCookieStore(sessionHashKey, sessionBlockKey)
	sessionStore.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 30,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   auth.SecureCookies(cfg.BaseURL),
	}

	apiKeyLookup := authmw.APIKeyAuthFunc(func(ctx context.Context, hashed string) (auth.APIKey, user.User, error) {
		key, err := authStore.GetByHash(ctx, hashed)
		if err != nil {
			return auth.APIKey{}, user.User{}, err
		}
		u, err := userSvc.GetByID(ctx, key.UserID)
		if err != nil {
			return auth.APIKey{}, user.User{}, err
		}
		return key, u, nil
	})

	// ── HTTP server ───────────────────────────────────────────────────────────
	srv := server.New(
		cfg,
		sessionStore,
		userSvc,
		ticketSvc,
		categorySvc,
		groupSvc,
		tagSvc,
		adminSvc,
		customFieldSvc,
		slaPolicySvc,
		pluginRegistry,
		apiKeyLookup,
		authStore,
		authStore,
		registrationSvc,
		cannedResponseSvc,
	)

	srv.InitSAML(ctx)
	if err := srv.InitOIDC(ctx); err != nil {
		slog.Warn("OIDC initialization failed at startup", "error", err)
	}

	// ── MCP server (mounted under /mcp) ───────────────────────────────────────
	// srv supplies the visibility rule: MCP applies the same authority model as
	// the REST API rather than a second copy of it.
	mcpSrv := mcp.New(ticketSvc, adminSvc.TicketPrefix, srv, categorySvc)

	mux := http.NewServeMux()
	// Wrapped, never bare: Handler() authenticates nothing on its own, and this
	// mux sits outside srv's middleware chain. Mounted directly, every MCP tool
	// was reachable with no credentials.
	mux.Handle("/mcp/", srv.ProtectMCP(mcpSrv.Handler()))
	mux.Handle("/api/", srv)
	mux.Handle("/health", srv)
	mux.Handle("/", server.NewSPAHandler(ui.FS()))

	httpSrv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	shutdownDone := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		shutdownDone <- httpSrv.Shutdown(shutdownCtx)
	}()

	slog.Info("go-help-desk listening", "port", cfg.HTTPPort)
	if err := httpSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}

	return <-shutdownDone
}
