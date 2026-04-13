package main

import (
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/sessions"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/open-help-desk/open-help-desk/backend/internal/config"
	"github.com/open-help-desk/open-help-desk/backend/internal/database"
	"github.com/open-help-desk/open-help-desk/backend/internal/database/adminstore"
	"github.com/open-help-desk/open-help-desk/backend/internal/database/auditstore"
	"github.com/open-help-desk/open-help-desk/backend/internal/database/authstore"
	"github.com/open-help-desk/open-help-desk/backend/internal/database/categorystore"
	"github.com/open-help-desk/open-help-desk/backend/internal/database/groupstore"
	"github.com/open-help-desk/open-help-desk/backend/internal/database/slastore"
	"github.com/open-help-desk/open-help-desk/backend/internal/database/ticketstore"
	"github.com/open-help-desk/open-help-desk/backend/internal/database/userstore"
	"github.com/open-help-desk/open-help-desk/backend/internal/dbgen"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/admin"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/auth"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/category"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/group"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/plugin"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/sla"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/ticket"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/user"
	authmw "github.com/open-help-desk/open-help-desk/backend/internal/middleware"
	"github.com/open-help-desk/open-help-desk/backend/internal/mcp"
	"github.com/open-help-desk/open-help-desk/backend/internal/server"
	"github.com/open-help-desk/open-help-desk/backend/internal/server/notify"
	"github.com/open-help-desk/open-help-desk/backend/internal/ui"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("fatal: %v", err)
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
		log.Printf("warn: invalid LOG_LEVEL %q, defaulting to info", cfg.LogLevel)
		logLevel = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})))

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

	// ── Domain services ───────────────────────────────────────────────────────
	userSvc := user.NewService(uStore)
	categorySvc := category.NewService(cStore)
	groupSvc := group.NewService(gStore)
	adminSvc := admin.NewService(aStore)

	var slaSvc *sla.Service
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

	// ── Plugin registry ───────────────────────────────────────────────────────
	pluginRegistry := plugin.NewRegistry()

	// ── Ticket service ────────────────────────────────────────────────────────
	ticketSvc := ticket.NewService(tStore, tStore, dispatcher, auStore, slaSvc)
	if err := ticketSvc.LoadSystemStatuses(ctx); err != nil {
		return fmt.Errorf("loading system statuses: %w", err)
	}

	// ── Auth helpers ──────────────────────────────────────────────────────────
	gob.Register(auth.SessionData{})
	sessionStore := sessions.NewCookieStore([]byte(cfg.SessionSecret))

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
		adminSvc,
		pluginRegistry,
		apiKeyLookup,
		authStore,
		authStore,
	)

	// ── MCP server (mounted under /mcp) ───────────────────────────────────────
	mcpSrv := mcp.New(ticketSvc)

	mux := http.NewServeMux()
	mux.Handle("/mcp/", mcpSrv.Handler())
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

	slog.Info("open-help-desk listening", "port", cfg.HTTPPort)
	if err := httpSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}

	return <-shutdownDone
}

