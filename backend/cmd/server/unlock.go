package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"os"
	osuser "os/user"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/crypto/bcrypt"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// unlockPasswordLength is well past the minimum (user.MinPasswordLength) and
// well under the bcrypt limit (user.MaxPasswordLength). 24 characters from a
// 57-character alphabet is about 140 bits.
const unlockPasswordLength = 24

// unlockAlphabet leaves out 0 O 1 l I: the password is read off a terminal and
// typed once, and a character that can be misread is a lockout all over again.
const unlockAlphabet = "23456789abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ"

// unlock is reset-factors plus everything else that keeps somebody out: a
// disabled account, a spent MFA budget, a password they cannot use. It puts
// the account back in a state the owner can sign in from, using nothing but
// shell access to the server, so it adds no remote attack surface (#497; see
// docs/DESIGN.md → Authentication → Passkeys, "The way back in").
//
// Everything below the lookup happens in ONE transaction, the audit entry
// included, and the password is printed only after it commits. A password the
// database never stored is worse than none, and an unlock nobody can see in
// the audit trail is exactly what #306 asked not to have. This is stricter
// than reset-factors, which logs and swallows an audit failure: that command
// changes nothing about how the account authenticates afterwards, this one
// hands out a credential.
//
// The password is returned through out and nowhere else: not slog, not the
// audit entry. It is shown once and the account must replace it at its next
// sign-in (users.must_change_password).
//
// The in-memory sign-in throttle is not touched. It lives in the server
// process, which this is not, and it only ever delays a login, never refuses
// the right password, so it cannot be what is keeping anybody out.
func unlock(ctx context.Context, email string, out io.Writer) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return fmt.Errorf("usage: go-help-desk unlock <email>")
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
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	// GetByEmail hides deleted accounts and matches exactly, which is the
	// refusal wanted: an unknown or deleted address is an error, not a guess.
	u, err := userstore.New(dbgen.New(sqlDB)).GetByEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("no account with the address %q: %w", email, err)
	}
	// TODO(#490): refuse an OAuth service account here ("service accounts have
	// no password to unlock") once that field exists on this branch's base.

	password, err := generatePassword()
	if err != nil {
		return err
	}
	// Hashed before the transaction opens: bcrypt at production cost is a
	// quarter of a second that should not be spent holding a row lock.
	hash, err := bcrypt.GenerateFromPassword([]byte(password), user.ProductionBcryptCost)
	if err != nil {
		return fmt.Errorf("hashing the new password: %w", err)
	}

	osUsername := "unknown"
	if osu, err := osuser.Current(); err == nil {
		osUsername = osu.Username
	}
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting the transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // a no-op once committed; the failure that matters is Commit's

	q := dbgen.New(sqlDB).WithTx(tx)
	users := userstore.New(q)

	// Factors, and every session with them, exactly as reset-factors does it.
	// Sessions go for the reason reset-factors spells out: a live one carries
	// MFAPassed from the factor just cleared and could register its own.
	removed, err := users.ClearFactors(ctx, u.ID)
	if err != nil {
		return fmt.Errorf("clearing their factors and ending their sessions: %w", err)
	}
	if err := users.Unlock(ctx, u.ID, string(hash)); err != nil {
		return err
	}

	// ActorID nil and "actor" spelled out: nobody signed in, and the console is
	// a fact where an invented user id would be a claim. The OS user and host
	// say who is at it. Neither the password nor its hash is recorded.
	if err := auditstore.New(q).Create(ctx, audit.Entry{
		ID:         uuid.New(),
		ActorID:    nil,
		EntityType: "user",
		EntityID:   u.ID,
		Action:     "account_unlock",
		After: map[string]any{
			"actor":                "server console",
			"source":               "cli",
			"os_user":              osUsername,
			"host":                 host,
			"passkeys_removed":     removed,
			"totp_cleared":         true,
			"sessions_revoked":     true,
			"was_disabled":         u.Disabled,
			"mfa_lockout_cleared":  true,
			"password_replaced":    true,
			"must_change_password": true,
		},
		CreatedAt: time.Now(),
	}); err != nil {
		return fmt.Errorf("writing the audit entry: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing: %w", err)
	}

	// To the writer given, not the structured log: somebody is watching this
	// run in a terminal, and a log line is a copy that outlives it.
	fmt.Fprintf(out,
		"Unlocked %s (%s).\n"+
			"  Every second factor cleared (passkeys removed: %d)\n"+
			"  Account enabled, MFA lockout cleared\n"+
			"  All sessions ended\n\n"+
			"  New password: %s\n\n"+
			"This is shown once and is not stored anywhere in readable form. They must\n"+
			"change it when they sign in; until they do, it is all the account can do.\n",
		u.Email, u.DisplayName, removed, password)
	return nil
}

// generatePassword draws unlockPasswordLength characters from unlockAlphabet
// with crypto/rand. big.Int rather than a modulo of random bytes, which would
// favour the first few letters.
func generatePassword() (string, error) {
	limit := big.NewInt(int64(len(unlockAlphabet)))
	b := make([]byte, unlockPasswordLength)
	for i := range b {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", fmt.Errorf("generating a password: %w", err)
		}
		b[i] = unlockAlphabet[n.Int64()]
	}
	return string(b), nil
}
