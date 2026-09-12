package ticket

import "github.com/google/uuid"

// Visibility says which tickets a listing may return. It is the actor's
// authority expressed as data, resolved once by the caller that knows the
// actor's role and the instance's scope setting, so the store does not need
// either.
type Visibility int

const (
	// VisibilityScoped is the DESIGN.md staff scope: tickets the actor
	// reported, is assigned, their groups are assigned, or that fall in a
	// Category/Type their groups cover.
	VisibilityScoped Visibility = iota
	// VisibilityReporter restricts a listing to tickets the actor reported.
	VisibilityReporter
	// VisibilityAll applies no visibility restriction.
	VisibilityAll
)

// Filter is the optional criteria a ticket listing may narrow by. A nil
// pointer means "not filtered by this", which is why they are pointers rather
// than zero values — uuid.Nil and "" are indistinguishable from absent.
type Filter struct {
	ActorID    uuid.UUID
	Visibility Visibility

	StatusID       *uuid.UUID
	Priority       *Priority
	CategoryID     *uuid.UUID
	AssigneeUserID *uuid.UUID
	Query          string

	Limit  int
	Offset int
}
