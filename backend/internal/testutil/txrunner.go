package testutil

import (
	"context"

	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// JoiningTxRunner implements ticket.Atomic by running fn against the caller's
// existing transaction, without beginning or committing one of its own.
//
// It exists because the integration harness already wraps each test in a
// transaction it rolls back at the end (see TxQueries), and every store shares
// that transaction's Queries. The production runner calls db.BeginTx, which
// would open a second transaction on a different connection — one that cannot
// see the harness transaction's uncommitted rows, so every ticket write in the
// suite would operate on data that appears not to exist.
//
// The trade is deliberate and worth naming: a test using this does NOT exercise
// commit or rollback, because there is nothing to commit into. Rollback
// behaviour is covered in the domain tests, which use a fake that really does
// undo its writes. What this preserves is that the service's transactional code
// path — the one production takes — is the path the HTTP tests run through.
//
// Test-only. Production wiring uses database/txrunner.
type JoiningTxRunner struct {
	q *dbgen.Queries
}

// NewJoiningTxRunner returns a ticket.Atomic bound to an existing transaction's
// Queries.
func NewJoiningTxRunner(q *dbgen.Queries) *JoiningTxRunner {
	return &JoiningTxRunner{q: q}
}

// InTx runs fn against the enclosing transaction.
func (r *JoiningTxRunner) InTx(_ context.Context, fn func(ticket.Store, audit.Store) error) error {
	return fn(ticketstore.New(r.q), auditstore.New(r.q))
}
