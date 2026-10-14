package server

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// failingGroupLookup makes GetByID fail with something other than "no such
// row". The embedded Store is left nil: no other call is made on the path it
// is used for.
type failingGroupLookup struct{ group.Store }

func (failingGroupLookup) GetByID(context.Context, uuid.UUID) (group.Group, error) {
	return group.Group{}, errors.New("injected group lookup failure")
}

// failingUserLookup is the same for user lookups.
type failingUserLookup struct{ user.Store }

func (failingUserLookup) GetByID(context.Context, uuid.UUID) (user.User, error) {
	return user.User{}, errors.New("injected user lookup failure")
}

// FailUserLookupForTest makes user lookups fail as a database outage would.
func (s *Server) FailUserLookupForTest() { s.users = user.NewService(failingUserLookup{}) }

// FailGroupLookupForTest makes group lookups fail as a database outage would,
// injected for the reason given on FailSAMLSpendForTest.
func (s *Server) FailGroupLookupForTest() { s.groups = group.NewService(failingGroupLookup{}) }

// failingSpend makes the SAML hand-over ledger write fail and leaves every
// other session call alone.
type failingSpend struct{ SessionStore }

func (failingSpend) SpendSAMLHandover(context.Context, string) (bool, error) {
	return false, errors.New("injected ledger failure")
}

// SetUsersForTest swaps the user service the handlers call, so a test can put
// a store that fails one write in front of the real one. The authentication
// middleware keeps the lookup it was built with.
func (s *Server) SetUsersForTest(u *user.Service) { s.users = u }

// failingDelete makes the session row delete fail, which is the first thing a
// session rewrite does for a session that already has an id.
type failingDelete struct{ SessionStore }

func (failingDelete) Delete(context.Context, string) error {
	return errors.New("injected session store failure")
}

// FailSessionWritesForTest makes a handler's rewrite of the signed-in session
// fail. The authentication middleware keeps the store it was built with, so
// the request still authenticates and only the handler's own write breaks.
func (s *Server) FailSessionWritesForTest() { s.sessions = failingDelete{s.sessions} }

// FailSAMLSpendForTest makes the single-use ledger write fail. It is injected
// here rather than by breaking the table: a SQL error aborts the harness's one
// shared transaction, so every later query would fail too and a handler that
// wrongly ignored the error would still end in a 500.
func (s *Server) FailSAMLSpendForTest() { s.sessions = failingSpend{s.sessions} }
