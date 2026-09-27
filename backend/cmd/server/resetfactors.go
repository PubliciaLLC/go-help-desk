package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/stdlib"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/webauthnstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
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
	passkeys := webauthnstore.New(q)

	// GetByEmail and not a search: this is a destructive act on one account,
	// and matching loosely is how the wrong person gets reset.
	u, err := users.GetByEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("no account with the address %q: %w", email, err)
	}

	creds, err := passkeys.ListByUser(ctx, u.ID)
	if err != nil {
		return fmt.Errorf("reading registered passkeys: %w", err)
	}
	for _, c := range creds {
		if err := passkeys.Delete(ctx, c.ID, u.ID); err != nil {
			return fmt.Errorf("removing passkey %s: %w", c.ID, err)
		}
	}
	if err := users.ClearMFA(ctx, u.ID); err != nil {
		return fmt.Errorf("clearing the authenticator: %w", err)
	}

	// Written to stdout rather than the structured log: somebody is watching
	// this run in a terminal, having probably just been locked out.
	fmt.Fprintf(os.Stdout,
		"Cleared every second factor from %s (%s).\n"+
			"  TOTP authenticator: cleared\n"+
			"  Passkeys removed:   %d\n\n"+
			"They can now sign in with their password and will be asked to enrol again.\n"+
			"Their password is unchanged, and no session was revoked — sign them out separately if that matters.\n",
		u.Email, u.DisplayName, len(creds))
	return nil
}
