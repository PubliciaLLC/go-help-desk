package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// A credential with nobody behind it is refused by the write tools, not
// handed a database error.
//
// MCP admits OAuth bearer tokens, and an OAuth client actor carries no user
// id. Every write tool records who acted — a ticket's reporter, a reply's
// author, an audit entry, a status-history row — so a nil id reached the
// insert and came back as a foreign-key violation: the caller was told
// "create ticket failed" and the operator's log filled with table and column
// names. create_ticket took a tracking number from the sequence first, so
// every attempt also left a hole in the numbering.
//
// The REST API was fixed for this two rounds ago and MCP was not, which is
// the failure mode a second surface always has: one rule, implemented twice.
func TestWriteTools_RefuseACredentialWithNoUserIdentity(t *testing.T) {
	machine := &authmw.Actor{Role: user.RoleStaff, Machine: true}

	if hasUserIdentity(machine) {
		t.Fatal("an actor with no user id was treated as a person")
	}
	if !hasUserIdentity(&authmw.Actor{Role: user.RoleStaff, UserID: uuid.New()}) {
		t.Fatal("a real staff actor was treated as a machine")
	}

	// The message has to say what is wrong. "create ticket failed" sent
	// people looking for a bug in their request.
	if !strings.Contains(noUserIdentityMessage, "user identity") {
		t.Errorf("the refusal does not say why: %q", noUserIdentityMessage)
	}
}

// create_follow_up records the acting member of staff in the new ticket's
// history and audit entry (#349), so it is held to the same rule: an OAuth
// client with no user behind it is refused before anything is looked up. A nil
// ticket service makes a missed check a panic rather than a passing test.
func TestCreateFollowUp_RefusesACredentialWithNoUserIdentity(t *testing.T) {
	s := &Server{}
	machine := &authmw.Actor{Role: user.RoleStaff, Machine: true}
	ctx := context.WithValue(context.Background(), actorCtxKey{}, machine)

	res, err := s.handleCreateFollowUp(ctx, callToolRequest("create_follow_up",
		map[string]any{"ticket_id": uuid.New().String()}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(resultText(t, res), "user identity") {
		t.Fatalf("a credential with no user identity must be refused for it; got %q", resultText(t, res))
	}
}
