package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

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

// PasskeyStartHandlersForTest returns the two passkey handlers that look the
// account up before doing anything else, so a test can drive them with an
// actor the router never produces (a service user, whose session cannot load).
func (s *Server) PasskeyStartHandlersForTest() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"login start":    s.handlePasskeyLoginStart,
		"register start": s.handlePasskeyRegisterStart,
	}
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

// FailReopenTargetCheckForTest makes the reopen-target check fail, as a status
// list that cannot be read would. Injected, not provoked: breaking the table
// aborts the harness's shared transaction and the status save before it.
func (s *Server) FailReopenTargetCheckForTest() {
	s.reopenTargetCheck = func(context.Context) (reopenTargetStatus, error) {
		return reopenTargetStatus{}, errors.New("injected status list failure")
	}
}

// RouteForTest is one registered route: its method and full path pattern.
type RouteForTest struct{ Method, Pattern string }

// RoutesForTest lists every route the router serves, so a test can drive them
// all instead of a hand-kept list that a new route would skip.
func (s *Server) RoutesForTest() []RouteForTest {
	var out []RouteForTest
	_ = chi.Walk(s.router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		out = append(out, RouteForTest{Method: method, Pattern: route})
		return nil
	})
	return out
}

// RoundRobinFirst is who the next round-robin create is offered first, for a
// test that takes turns concurrently.
func (s *Server) RoundRobinFirst(uids []uuid.UUID) uuid.UUID {
	return *s.roundRobin(uids)[0].UserID
}

// AuditAPIKeyForTest writes a key audit entry with the context it is given, so
// a test can hand it one that is already cancelled.
func (s *Server) AuditAPIKeyForTest(ctx context.Context, actor uuid.UUID, action string, keyID, ownerID uuid.UUID) {
	s.auditAPIKey(ctx, actor, action, keyID, ownerID, []string{"tickets:read"}, nil)
}
