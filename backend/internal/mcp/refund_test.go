package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// failingStore refuses at the first store call create() makes, which is after
// the resolver has spent the caller's hourly budget.
type failingStore struct{ ticket.Store }

func (failingStore) CategoryExists(context.Context, uuid.UUID) (bool, error) {
	return false, errors.New("database is down")
}

type spendingResolver struct{ refunded int }

func (r *spendingResolver) ResolveOnBehalf(_ context.Context, actor uuid.UUID, in *ticket.CreateInput,
	_ *uuid.UUID, email *string, _, _ string) (func(), error) {
	in.GuestEmail = email
	in.GuestName = "Ada"
	in.FiledBy = &actor
	return func() { r.refunded++ }, nil
}

// A ticket that is not filed gives the budget back (#444): the slot was spent
// before Create so parallel requests cannot overshoot, and a failed Create
// mailed nobody.
func TestCreateTicket_ReporterEmail_RefundsTheBudgetWhenNothingIsFiled(t *testing.T) {
	res := &spendingResolver{}
	s := &Server{
		tickets:  ticket.NewService(failingStore{}, nil, nil, nil, nil, nil),
		prefix:   func(context.Context) string { return "GHD" },
		onBehalf: res,
	}
	actor := &authmw.Actor{Role: user.RoleStaff, UserID: uuid.New()}
	ctx := context.WithValue(context.Background(), actorCtxKey{}, actor)

	out, err := s.handleCreateTicket(ctx, callToolRequest("create_ticket", map[string]any{
		"subject": "x", "category_id": uuid.NewString(), "reporter_email": "walk-in@example.test",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !out.IsError {
		t.Fatalf("the create was meant to fail; got %q", resultText(t, out))
	}
	if res.refunded != 1 {
		t.Fatalf("the spent slot was refunded %d times, want once", res.refunded)
	}
}
