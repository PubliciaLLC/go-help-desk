package mcp

import (
	"context"
	"testing"

	"github.com/google/uuid"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// Reporting users reach MCP as of this change, so the transport no longer
// decides what a caller may do. These tests pin the two rules that replaced it:
// writes require staff, and reads are filtered through the Authorizer.
//
// As in server_test.go, s.tickets is nil wherever a handler must refuse before
// touching it — a nil service turns a missed check into a panic rather than a
// silently passing test.

func ctxAs(role user.Role) context.Context {
	a := &authmw.Actor{UserID: uuid.New(), Role: role, MFAPassed: true}
	return context.WithValue(context.Background(), actorCtxKey{}, a)
}

func TestWriteTools_RefuseReportingUsers(t *testing.T) {
	s := &Server{} // nil ticket service on purpose

	ticketID := uuid.New().String()

	cases := []struct {
		name string
		call func(context.Context) (*mcpgo.CallToolResult, error)
	}{
		{"create_ticket", func(ctx context.Context) (*mcpgo.CallToolResult, error) {
			return s.handleCreateTicket(ctx, callToolRequest("create_ticket", map[string]any{
				"subject": "Filed by a reporting user", "category_id": uuid.New().String(),
			}))
		}},
		{"add_reply", func(ctx context.Context) (*mcpgo.CallToolResult, error) {
			return s.handleAddReply(ctx, callToolRequest("add_reply", map[string]any{
				"ticket_id": ticketID, "body": "hello",
			}))
		}},
		{"assign_ticket", func(ctx context.Context) (*mcpgo.CallToolResult, error) {
			return s.handleAssignTicket(ctx, callToolRequest("assign_ticket", map[string]any{
				"ticket_id": ticketID, "assignee_user_id": uuid.New().String(),
			}))
		}},
		{"update_ticket_status", func(ctx context.Context) (*mcpgo.CallToolResult, error) {
			return s.handleUpdateTicketStatus(ctx, callToolRequest("update_ticket_status", map[string]any{
				"ticket_id": ticketID, "status_id": uuid.New().String(),
			}))
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := tc.call(ctxAs(user.RoleUser))
			require.NoError(t, err, "refusal is reported in the result, not as a transport error")
			require.True(t, res.IsError, "%s must refuse a reporting user", tc.name)
			require.Contains(t, resultText(t, res), "staff",
				"the refusal must say a staff account is needed")
		})
	}
}

// recordingAuthorizer captures what the handlers ask it, and answers as told.
type recordingAuthorizer struct {
	visibility ticket.Visibility
	allow      bool
	asked      int
}

func (r *recordingAuthorizer) CanViewTicket(context.Context, *authmw.Actor, ticket.Ticket) (bool, error) {
	r.asked++
	return r.allow, nil
}

func (r *recordingAuthorizer) TicketVisibility(context.Context, *authmw.Actor) (ticket.Visibility, error) {
	return r.visibility, nil
}

// A missing Authorizer must deny, not permit. This is the fail-closed guard on
// the constructor: the original defect was a surface that served whoever asked.
func TestNew_WithoutAuthorizer_DeniesByDefault(t *testing.T) {
	s := New(nil, nil, nil, nil)

	require.False(t, s.visible(ctxAs(user.RoleAdmin), ticket.Ticket{}),
		"no Authorizer must mean nothing is visible")

	vis, err := s.authz.TicketVisibility(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, ticket.VisibilityReporter, vis,
		"the fallback listing mode must be the narrowest one")
}

// The Authorizer's answer, not the caller's role, decides a read. An admin who
// the Authorizer rejects gets the same not-found a stranger would.
func TestVisible_HonoursAuthorizer(t *testing.T) {
	denying := &recordingAuthorizer{allow: false}
	s := &Server{authz: denying}
	require.False(t, s.visible(ctxAs(user.RoleAdmin), ticket.Ticket{}))
	require.Equal(t, 1, denying.asked, "the handler must actually consult the Authorizer")

	allowing := &recordingAuthorizer{allow: true}
	s = &Server{authz: allowing}
	require.True(t, s.visible(ctxAs(user.RoleUser), ticket.Ticket{}))
}

func TestBuildListFilter_ClampsLimit(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
		want int
	}{
		{"default when absent", map[string]any{}, 20},
		{"under the ceiling is honoured", map[string]any{"limit": float64(50)}, 50},
		{"at the ceiling", map[string]any{"limit": float64(100)}, 100},
		{"above the ceiling is clamped", map[string]any{"limit": float64(5000)}, maxListLimit},
		{"zero floors to one", map[string]any{"limit": float64(0)}, 1},
		{"negative floors to one", map[string]any{"limit": float64(-7)}, 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := buildListFilter(tc.args, ticket.VisibilityAll, uuid.New())
			require.NoError(t, err)
			require.Equal(t, tc.want, f.Limit)
		})
	}
}

// Offset must survive clamping: the ceiling bounds one page, it must not bound
// how far a caller can walk the result set.
func TestBuildListFilter_OffsetWindowsPastTheCeiling(t *testing.T) {
	f, err := buildListFilter(map[string]any{
		"limit": float64(500), "offset": float64(900),
	}, ticket.VisibilityAll, uuid.New())
	require.NoError(t, err)
	require.Equal(t, maxListLimit, f.Limit)
	require.Equal(t, 900, f.Offset, "offset must not be clamped")
}

// Visibility comes from the Authorizer, never from the arguments. Naming
// someone else as the assignee must not widen what a reporting user can see.
func TestBuildListFilter_CallerCannotWidenVisibility(t *testing.T) {
	actor := uuid.New()
	f, err := buildListFilter(map[string]any{
		"assignee_user_id": uuid.New().String(),
	}, ticket.VisibilityReporter, actor)
	require.NoError(t, err)
	require.Equal(t, ticket.VisibilityReporter, f.Visibility,
		"a reporting user must never be listed at a wider visibility")
	require.Equal(t, actor, f.ActorID,
		"the scope actor is the authenticated caller, not an argument")
}

func TestBuildListFilter_AcceptsFilters(t *testing.T) {
	statusID, categoryID, assignee := uuid.New(), uuid.New(), uuid.New()
	f, err := buildListFilter(map[string]any{
		"status_id":        statusID.String(),
		"category_id":      categoryID.String(),
		"assignee_user_id": assignee.String(),
		"priority":         "high",
		"q":                "printer",
	}, ticket.VisibilityAll, uuid.New())
	require.NoError(t, err)
	require.Equal(t, &statusID, f.StatusID)
	require.Equal(t, &categoryID, f.CategoryID)
	require.Equal(t, &assignee, f.AssigneeUserID)
	require.NotNil(t, f.Priority)
	require.Equal(t, ticket.PriorityHigh, *f.Priority)
	require.Equal(t, "printer", f.Query)
}

// An absent filter must be nil, not a zero value: uuid.Nil is a real UUID and
// would silently match nothing instead of meaning "any".
func TestBuildListFilter_AbsentFiltersAreNil(t *testing.T) {
	f, err := buildListFilter(map[string]any{}, ticket.VisibilityAll, uuid.New())
	require.NoError(t, err)
	require.Nil(t, f.StatusID)
	require.Nil(t, f.CategoryID)
	require.Nil(t, f.AssigneeUserID)
	require.Nil(t, f.Priority)
	require.Empty(t, f.Query)
}

func TestBuildListFilter_RejectsBadFilters(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"priority", map[string]any{"priority": "urgent"}, "priority must be one of"},
		{"status_id", map[string]any{"status_id": "not-a-uuid"}, "invalid status_id"},
		{"category_id", map[string]any{"category_id": "not-a-uuid"}, "invalid category_id"},
		{"assignee_user_id", map[string]any{"assignee_user_id": "not-a-uuid"}, "invalid assignee_user_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildListFilter(tc.args, ticket.VisibilityAll, uuid.New())
			require.Error(t, err, "a malformed filter must be reported, not ignored")
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestRequireStaff(t *testing.T) {
	require.True(t, requireStaff(&authmw.Actor{Role: user.RoleAdmin}))
	require.True(t, requireStaff(&authmw.Actor{Role: user.RoleStaff}))
	require.False(t, requireStaff(&authmw.Actor{Role: user.RoleUser}))
	require.False(t, requireStaff(nil), "no actor is never staff")
}

// A ticket the caller may not see must be indistinguishable from one that does
// not exist. If the store's wording ever changes, this fails and notFoundFor
// must be changed with it — otherwise get_ticket becomes an oracle telling a
// caller which tracking numbers are real.
func TestNotFoundFor_MatchesStoreWording(t *testing.T) {
	id := uuid.New()

	// The other half of this pin lives in the database package's
	// TestGetByID_NotFoundWording, which asserts a real missing-ticket lookup
	// produces this same string. Both must agree for the two cases to be
	// indistinguishable; if either drifts, one of the two tests fails.
	require.Equal(t, "not found: ticket "+id.String(), notFoundFor(id.String()))
}
