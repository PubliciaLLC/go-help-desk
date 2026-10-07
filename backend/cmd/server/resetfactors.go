package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	osuser "os/user"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
)

// resetFactors clears every second factor from one account, so its owner can
// sign in with their password and enrol again.
//
// This exists because the web path must refuse to do it. A session that has
// not passed MFA cannot add or remove a factor on an account that already has
// one — otherwise somebody holding only the password registers their own key
// and thereby obtains the second factor, which is passing the gate rather
// than breaking it. That refusal is correct and it leaves one situation with
// no way out: an administrator whose registered factor is lost is recovered
// by another administrator, and a SOLE administrator has no other
// administrator. Setup does not reopen, so the instance would be orphaned by
// one lost phone.
//
// A command on the server is the right channel precisely because it grants
// nothing. Whoever can run this already holds the filesystem and the database
// credentials, which is to say they already hold everything; it widens no
// web-facing surface. A recovery code would have — another secret at rest,
// worth stealing, which is the property passkeys exist to remove.
//
// See docs/DESIGN.md → Authentication → Passkeys (WebAuthn).
func resetFactors(ctx context.Context, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return fmt.Errorf("usage: go-help-desk reset-factors <email>")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	pool, err := database.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connecting to the database: %w", err)
	}
	defer pool.Close()
	// sqlc wants a database/sql-compatible handle; wrap the pool, as the
	// server does.
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	q := dbgen.New(sqlDB)
	users := userstore.New(q)
	auStore := auditstore.New(q)

	// GetByEmail and not a search: this is a destructive act on one account,
	// and matching loosely is how the wrong person gets reset.
	u, err := users.GetByEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("no account with the address %q: %w", email, err)
	}

	// Every factor gone and every session ended, in ONE statement.
	//
	// This was three: delete the passkeys, clear the TOTP columns, delete the
	// sessions. A failure between the second and the third left the account
	// with no factor and its MFAPassed=true sessions alive, which is exactly
	// the state ending the sessions exists to prevent (#307 item 4). One
	// statement is all or nothing, so a failure leaves the account as it was
	// and the same command can be run again.
	//
	// The sessions are part of it, not tidiness. A live session carries
	// MFAPassed=true from the moment it passed the factor just cleared, and
	// requireFactorOrFirstEnrolment answers that flag FIRST, before it asks
	// whether the account is protected. So a session that survives this
	// command can register its own passkey, which is the exact thing the web
	// path refuses to let a password alone do, granted instead by the act of
	// recovering the owner. Whoever holds a stolen cookie would get a durable
	// factor out of somebody else's recovery, and could then lock the real
	// owner out of it. handler_admin_users.go's reset has the same duty.
	//
	// Found by the session-B review of #302.
	removed, err := users.ClearFactors(ctx, u.ID)
	if err != nil {
		return fmt.Errorf("clearing their factors and ending their sessions: %w", err)
	}

	// One audit entry for the whole recovery action. ActorID is nil — nobody
	// signed in to do this — but nil-actor is not the same fact as "we don't
	// know who": the OS user and host say who ran the command, and "source":
	// "cli" says plainly that this came from the server rather than a
	// session, which is the distinction #306 asked to preserve rather than
	// flatten into the same shape as an admin's web-driven reset.
	//
	// Logged and swallowed on failure rather than returned: the recovery
	// itself already succeeded by this point, and an audit write failing must
	// not be reported as if the recovery had — the same trade-off
	// Service.writeAuditEntry makes for the two web paths.
	osUsername := "unknown"
	if osu, err := osuser.Current(); err == nil {
		osUsername = osu.Username
	}
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	if err := auStore.Create(ctx, audit.Entry{
		ID:         uuid.New(),
		ActorID:    nil,
		EntityType: "user",
		EntityID:   u.ID,
		Action:     "mfa_reset",
		After: map[string]any{
			"source":           "cli",
			"os_user":          osUsername,
			"host":             host,
			"passkeys_removed": removed,
			"totp_cleared":     true,
			"sessions_revoked": true,
		},
		CreatedAt: time.Now(),
	}); err != nil {
		slog.Error("writing audit entry for reset-factors failed", "email", email, "error", err)
	}

	// Written to stdout rather than the structured log: somebody is watching
	// this run in a terminal, having probably just been locked out.
	fmt.Fprintf(os.Stdout,
		"Cleared every second factor from %s (%s).\n"+
			"  TOTP authenticator: cleared\n"+
			"  Passkeys removed:   %d\n\n"+
			"  Sessions revoked:   all of them\n\n"+
			"They can now sign in with their password and will be asked to enrol again.\n"+
			"Their password is unchanged. They are signed out everywhere and must sign in again.\n",
		u.Email, u.DisplayName, removed)
	return nil
}
