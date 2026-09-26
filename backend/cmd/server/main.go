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
	"github.com/publiciallc/go-help-desk/backend/internal/database/reputationstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/sessionstore"
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

// slaSweepInterval is how often the breach sweep runs. Policy targets are
// integer minutes (response_target_min / resolution_target_min), and the
// sweep stamps the time a breach was DETECTED rather than the deadline
// instant, so this interval is exactly the stamp's worst-case error. One
// minute keeps that error at the same granularity as the targets themselves:
// 30s would double the query load for sub-minute precision no target can
// express, and 5m would let a 15-minute critical response target report a
// breach up to 33% late. Not configurable — DESIGN.md names no setting for
// it, and the candidate query's own age prefilter keeps the load bounded
// regardless of interval.
const slaSweepInterval = time.Minute

// runSLASweepTick runs one breach-sweep pass if and only if enabled(ctx)
// reports the SLA feature on — read fresh on every call, not decided once at
// boot, per gatedSLA's doc comment in sla_wiring.go. ran reports whether
// sweep was actually invoked, so a disabled tick can be told apart from an
// enabled tick that swept zero candidates.
//
// Extracted out of the goroutine in run() so this gating is unit-testable
// (see sla_wiring_test.go) without booting a database or an HTTP server:
// enabled and sweep are both narrow function values, not the concrete
// admin.Service / sla.Service types.
func runSLASweepTick(
	ctx context.Context,
	enabled func(ctx context.Context) bool,
	sweep func(ctx context.Context, now time.Time) (sla.SweepResult, error),
	now time.Time,
) (ran bool, res sla.SweepResult, err error) {
	if !enabled(ctx) {
		return false, sla.SweepResult{}, nil
	}
	res, err = sweep(ctx, now)
	return true, res, err
}

// autoCloseBatch is the page size for the auto-close sweep. Resolved tickets
// past the reopen window flip to Closed on each tick.
const autoCloseBatch = 500

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
	repStore := reputationstore.New(q)

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

	// slaSvc is always wired into the ticket service now — never nil. What
	// used to gate it (cfg.SLAEnabled, decided once here at boot) and what
	// actually governs the feature (adminSvc.SLAEnabled, the Settings-page
	// toggle DESIGN.md documents as the real switch) used to be two
	// independent gates: an admin turning the UI toggle on, with the env var
	// at its default of false, attached no records and ran no sweep until the
	// process was restarted. gatedSLA closes that gap by checking the DB
	// setting live, on every call, so main.go no longer decides this at all.
	// See gatedSLA's doc comment in sla_wiring.go.
	//
	// loggingSLA still wraps the outside, so a real failure from the inner
	// sla.Service (as opposed to gatedSLA's own no-op when the feature is
	// off) is still reported — the ticket service treats these as non-fatal
	// and drops them, and domain code does not log, so without this wrapper
	// they would vanish entirely.
	slaSvc := ticket.SLAService(newLoggingSLA(newGatedSLA(sla.NewService(slStore), adminSvc), slog.Default()))

	// SLA_ENABLED is a startup-time convenience only: DESIGN.md documents it
	// as a way to pre-enable the feature so a fresh instance works from first
	// boot, without an admin needing to find the Settings toggle first. It
	// only ever turns the DB setting ON; it never turns it off, so it can
	// never override an admin's own choice to enable the feature via the UI
	// (or, for that matter, a previous run's pre-enable) with an unset or
	// false env var on a later boot.
	if cfg.SLAEnabled {
		if err := adminSvc.SetBool(ctx, admin.KeySLAEnabled, true); err != nil {
			return fmt.Errorf("pre-enabling SLA tracking: %w", err)
		}
	}

	// ── Notifications ─────────────────────────────────────────────────────────
	emailDisp, err := notify.NewEmailDispatcher(cfg)
	if err != nil {
		return fmt.Errorf("initialising email dispatcher: %w", err)
	}
	webhookDisp := notify.NewWebhookDispatcher(authStore, cfg.BaseURL, slog.Default())
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
	// Server-side sessions, not a cookie store. The cookie carries an opaque
	// id and the state lives in Postgres, which is what lets a session be
	// revoked: disable, role change, MFA reset and logout all become a DELETE
	// that takes effect on the next request, rather than waiting out the
	// cookie's lifetime.
	//
	// Seven days rather than thirty. Revocation is the real fix, but a shorter
	// absolute lifetime bounds the case nobody noticed. There is no idle
	// timeout: refreshing it would mean re-saving on every request.
	sessionStore := sessionstore.New(q, sessionHashKey, sessionBlockKey, &sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 7,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   auth.SecureCookies(cfg.BaseURL),
	})

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
		// The verdict cache. The provider and the API key are not passed:
		// both are settings an operator can change while this process runs,
		// so they are read per request. The Budget that holds the counters is
		// built inside New and lives as long as the server, which is the half
		// that must not be per request.
		server.WithReputationLookup(repStore),
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

	// Expired rows stop authenticating the moment they expire — GetSession
	// filters on expires_at — so this is housekeeping, not a security control.
	// Without it the table grows by one row per login forever, and the OIDC
	// login redirect writes a row per cookieless hit before anyone has
	// authenticated at all.
	sweepCtx, stopSweep := context.WithCancel(ctx)
	defer stopSweep()
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-sweepCtx.Done():
				return
			case <-t.C:
				n, err := sessionStore.DeleteExpired(sweepCtx)
				if err != nil {
					slog.WarnContext(sweepCtx, "sweeping expired sessions failed", "error", err)
					continue
				}
				if n > 0 {
					slog.InfoContext(sweepCtx, "swept expired sessions", "count", n)
				}
			}
		}
	}()

	// Breach stamps are facts about time passing, so like session expiry they
	// cannot be computed on read: a ticket nobody touches still breaches.
	// See DESIGN.md → SLA Tracking → Breach Evaluation.
	//
	// Always started — never gated on cfg.SLAEnabled — because whether the
	// sweep actually does anything on a given tick is decided live, inside
	// runSLASweepTick, by the same admin Settings toggle everything else SLA
	// now reads (see slaSvc's wiring above). Gating the goroutine itself on
	// the env var was the other half of the two-disconnected-gates bug: an
	// admin enabling SLA tracking only through the Settings UI got a sweep
	// that never ran until the process was restarted with SLA_ENABLED=true.
	go func() {
		t := time.NewTicker(slaSweepInterval)
		defer t.Stop()
		for {
			select {
			case <-sweepCtx.Done():
				return
			case <-t.C:
				ran, res, err := runSLASweepTick(sweepCtx, adminSvc.SLAEnabled,
					func(ctx context.Context, now time.Time) (sla.SweepResult, error) {
						return slaPolicySvc.SweepBreaches(ctx, tStore, now)
					}, time.Now())
				if !ran {
					continue
				}
				if err != nil {
					// Partial failures are joined; res is still meaningful.
					slog.WarnContext(sweepCtx, "sweeping SLA breaches failed", "error", err,
						"evaluated", res.Evaluated, "stamped", res.Stamped)
				}
				if res.Stamped > 0 {
					slog.InfoContext(sweepCtx, "stamped SLA breaches",
						"evaluated", res.Evaluated, "stamped", res.Stamped)
				}
			}
		}
	}()

	// Resolved tickets past the reopen window flip to Closed here, not on read:
	// DESIGN.md → Ticket Lifecycle → Auto-close scheduling. Five minutes because
	// the window is denominated in days; the interval is not a setting.
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-sweepCtx.Done():
				return
			case <-t.C:
				// Read per tick, never cached: the window is "as configured at
				// the time the sweep runs".
				days := adminSvc.ReopenWindowDays(sweepCtx)
				n, err := ticketSvc.AutoClose(sweepCtx, days, autoCloseBatch)
				if err != nil {
					slog.WarnContext(sweepCtx, "auto-closing resolved tickets failed", "closed", n, "error", err)
				}
				if n > 0 {
					slog.InfoContext(sweepCtx, "auto-closed resolved tickets", "count", n, "reopen_window_days", days)
				}
			}
		}
	}()

	httpSrv := &http.Server{
		Addr: fmt.Sprintf(":%d", cfg.HTTPPort),
		// Wraps everything, including the SPA: the orphaned pre-rename cookie
		// should be cleared on whatever request the browser makes first.
		Handler:      authmw.ExpireLegacySession(auth.SecureCookies(cfg.BaseURL), mux),
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
