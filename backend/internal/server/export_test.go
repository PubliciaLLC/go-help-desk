package server

import (
	"context"
	"errors"
)

// failingSpend makes the SAML hand-over ledger write fail and leaves every
// other session call alone.
type failingSpend struct{ SessionStore }

func (failingSpend) SpendSAMLHandover(context.Context, string) (bool, error) {
	return false, errors.New("injected ledger failure")
}

// FailSAMLSpendForTest makes the single-use ledger write fail. It is injected
// here rather than by breaking the table: a SQL error aborts the harness's one
// shared transaction, so every later query would fail too and a handler that
// wrongly ignored the error would still end in a 500.
func (s *Server) FailSAMLSpendForTest() { s.sessions = failingSpend{s.sessions} }
