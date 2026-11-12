package mcp

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// list_tickets takes the filters GET /tickets takes, through the same domain
// parse (ticket.ParseListFilter) and the same check (Filter.AllowedFor). These
// tests hold the MCP side of that: what an argument becomes, and what each kind
// of caller is refused. The REST side is in internal/server.

func TestBuildListFilter_NewFiltersMapThrough(t *testing.T) {
	reporter, typeID, itemID, group := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	s1, s2 := uuid.New(), uuid.New()
	after := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	before := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	f, err := buildListFilter(map[string]any{
		"reporter_id":       reporter.String(),
		"requester":         "  whitfield ",
		"created_after":     after.Format(time.RFC3339),
		"created_before":    before.Format(time.RFC3339),
		"updated_after":     after.Format(time.RFC3339),
		"updated_before":    before.Format(time.RFC3339),
		"priority":          []any{"high", "critical"},
		"type_id":           typeID.String(),
		"item_id":           itemID.String(),
		"assignee_group_id": group.String(),
		"status_ids":        []any{s1.String(), s2.String()},
		"include_closed":    false,
		"source_channel":    []any{"web", "mcp"},
		"sla":               "breached",
		"sort":              "subject",
		"order":             "desc",
		"q":                 "printer",
		"search_replies":    true,
	}, ticket.VisibilityAll, uuid.New())
	require.NoError(t, err)

	require.Equal(t, &reporter, f.ReporterID)
	require.Equal(t, "whitfield", f.Requester, "trimmed by the parser")
	require.Equal(t, after, f.CreatedAfter.UTC())
	require.Equal(t, before, f.CreatedBefore.UTC())
	require.Equal(t, after, f.UpdatedAfter.UTC())
	require.Equal(t, before, f.UpdatedBefore.UTC())
	require.Equal(t, []ticket.Priority{ticket.PriorityHigh, ticket.PriorityCritical}, f.AllPriorities())
	require.Equal(t, &typeID, f.TypeID)
	require.Equal(t, &itemID, f.ItemID)
	require.Equal(t, &group, f.AssigneeGroupID)
	require.Equal(t, []uuid.UUID{s1, s2}, f.AllStatusIDs())
	require.False(t, f.ExcludeClosed, "an explicit status wins over include_closed=false")
	require.Equal(t, []ticket.SourceChannel{ticket.ChannelWeb, ticket.ChannelMCP}, f.SourceChannels)
	require.Equal(t, ticket.SLABreached, f.SLA)
	require.Equal(t, ticket.SortSubject, f.Sort)
	require.Equal(t, ticket.OrderDesc, f.Order)
	require.Equal(t, "printer", f.Query)
	require.True(t, f.SearchReplies)
}

func TestBuildListFilter_IncludeClosedFalseLeavesOutClosed(t *testing.T) {
	f, err := buildListFilter(map[string]any{"include_closed": false}, ticket.VisibilityAll, uuid.New())
	require.NoError(t, err)
	require.True(t, f.ExcludeClosed)

	f, err = buildListFilter(map[string]any{}, ticket.VisibilityAll, uuid.New())
	require.NoError(t, err)
	require.False(t, f.ExcludeClosed, "closed tickets are listed unless asked otherwise, as before")
}

// status_id (one) predates status_ids and keeps working, alone and beside it.
func TestBuildListFilter_StatusIDStillWorksBesideStatusIDs(t *testing.T) {
	one, two := uuid.New(), uuid.New()

	f, err := buildListFilter(map[string]any{"status_id": one.String()}, ticket.VisibilityAll, uuid.New())
	require.NoError(t, err)
	require.Equal(t, &one, f.StatusID)
	require.Equal(t, []uuid.UUID{one}, f.AllStatusIDs())

	f, err = buildListFilter(map[string]any{
		"status_id": one.String(), "status_ids": []any{two.String(), one.String()},
	}, ticket.VisibilityAll, uuid.New())
	require.NoError(t, err)
	require.ElementsMatch(t, []uuid.UUID{one, two}, f.AllStatusIDs(), "both forms are ORed, and an id named twice counts once")
}

func TestBuildListFilter_PriorityIsAStringOrAnArray(t *testing.T) {
	f, err := buildListFilter(map[string]any{"priority": "low"}, ticket.VisibilityAll, uuid.New())
	require.NoError(t, err)
	require.Equal(t, []ticket.Priority{ticket.PriorityLow}, f.AllPriorities())

	f, err = buildListFilter(map[string]any{"priority": []any{"low", "medium"}}, ticket.VisibilityAll, uuid.New())
	require.NoError(t, err)
	require.Equal(t, []ticket.Priority{ticket.PriorityLow, ticket.PriorityMedium}, f.AllPriorities())
}

func TestBuildListFilter_RejectsWhatTheParserRejects(t *testing.T) {
	many := make([]any, 21)
	for i := range many {
		many[i] = uuid.NewString()
	}
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"unknown sort", map[string]any{"sort": "bogus"}, "invalid sort"},
		{"relevance is not a sort a caller may ask for", map[string]any{"sort": "relevance"}, "invalid sort"},
		{"unknown order", map[string]any{"sort": "number", "order": "up"}, "invalid order"},
		{"unknown order without a sort", map[string]any{"order": "up"}, "invalid order"},
		{"sla", map[string]any{"sla": "late"}, "invalid sla"},
		{"source_channel", map[string]any{"source_channel": []any{"carrier-pigeon"}}, "invalid source_channel"},
		{"status_ids", map[string]any{"status_ids": []any{"nope"}}, "invalid status_id"},
		{"too many status_ids", map[string]any{"status_ids": many}, "too many status_ids values (max 20)"},
		{"reporter_id", map[string]any{"reporter_id": "nope"}, "invalid reporter_id"},
		{"type_id", map[string]any{"type_id": "nope"}, "invalid type_id"},
		{"item_id", map[string]any{"item_id": "nope"}, "invalid item_id"},
		{"assignee_group_id", map[string]any{"assignee_group_id": "nope"}, "invalid assignee_group_id"},
		{"created_after", map[string]any{"created_after": "yesterday"}, "invalid created_after"},
		{"updated_before", map[string]any{"updated_before": "yesterday"}, "invalid updated_before"},
		{"a range that runs backwards", map[string]any{
			"created_after": "2026-04-01T00:00:00Z", "created_before": "2026-03-01T00:00:00Z",
		}, "created_after must be before created_before"},
		{"requester too short", map[string]any{"requester": "x"}, "requester must be 2 to 254 characters"},
		// A value of the wrong JSON type is refused, not skipped: skipping a
		// filter returns more than the caller asked to narrow to.
		{"a number for an id", map[string]any{"status_id": 5.0}, "invalid status_id"},
		{"a number in a list of ids", map[string]any{"status_ids": []any{5.0}}, "invalid status_id"},
		{"a string for a bool", map[string]any{"search_replies": "yes"}, "invalid search_replies"},
		{"a string for include_closed", map[string]any{"include_closed": "no"}, "invalid include_closed"},
		{"a number for sort", map[string]any{"sort": 3.0}, "invalid sort"},
		{"a string for limit", map[string]any{"limit": "100"}, "invalid limit"},
		{"a bool for limit", map[string]any{"limit": true}, "invalid limit"},
		{"a string for offset", map[string]any{"offset": "5"}, "invalid offset"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildListFilter(tc.args, ticket.VisibilityAll, uuid.New())
			require.Error(t, err)
			require.ErrorIs(t, err, ticket.ErrValidation, "a caller's mistake, so MCP answers it and does not log it")
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

func actorWith(role user.Role, machine bool, scopes ...string) *authmw.Actor {
	return &authmw.Actor{UserID: uuid.New(), Role: role, Machine: machine, Scopes: scopes}
}

// A requester (and any role the code does not recognise) may use only the
// filters and sorts on their allowed list. The rest would read a field they are
// never sent.
func TestListFilterFor_RequesterCannotUseStaffOnlyFilters(t *testing.T) {
	staffOnly := map[string]map[string]any{
		"reporter_id":       {"reporter_id": uuid.NewString()},
		"requester":         {"requester": "whitfield"},
		"updated_after":     {"updated_after": "2026-03-01T00:00:00Z"},
		"updated_before":    {"updated_before": "2026-03-01T00:00:00Z"},
		"assignee_user_id":  {"assignee_user_id": uuid.NewString()},
		"assignee_group_id": {"assignee_group_id": uuid.NewString()},
		"source_channel":    {"source_channel": []any{"web"}},
		"sla":               {"sla": "breached"},
		"sort by updated":   {"sort": "updated"},
		"sort by sla":       {"sort": "sla"},
	}
	for _, role := range []user.Role{user.RoleUser, user.Role("somebody-new")} {
		for name, args := range staffOnly {
			t.Run(string(role)+"/"+name, func(t *testing.T) {
				_, err := listFilterFor(actorWith(role, false), ticket.VisibilityReporter, args)
				var fe *ticket.FilterError
				require.ErrorAs(t, err, &fe)
				require.Equal(t, ticket.FilterForbidden, fe.Kind)
				require.Contains(t, fe.Msg, "not available to requesters")
			})
		}
	}

	t.Run("what a requester may use still works", func(t *testing.T) {
		_, err := listFilterFor(actorWith(user.RoleUser, false), ticket.VisibilityReporter, map[string]any{
			"status_ids": []any{uuid.NewString()}, "priority": []any{"high"}, "category_id": uuid.NewString(),
			"type_id": uuid.NewString(), "item_id": uuid.NewString(), "include_closed": false,
			"created_after": "2026-03-01T00:00:00Z", "sort": "number", "order": "asc", "q": "printer",
		})
		require.NoError(t, err)
	})
}

// Free text over every ticket a credential can see maps names and addresses to
// tickets in bulk, so an API key or OAuth client needs requester_search:read,
// as it does over REST. A person at a session does not.
func TestListFilterFor_RequesterTextNeedsTheScopeOnAMachineCredential(t *testing.T) {
	args := map[string]any{"requester": "whitfield"}
	refused := "this credential does not carry the requester_search:read scope"

	for _, role := range []user.Role{user.RoleStaff, user.RoleAdmin} {
		t.Run(string(role), func(t *testing.T) {
			_, err := listFilterFor(actorWith(role, true, "tickets:read"), ticket.VisibilityAll, args)
			var fe *ticket.FilterError
			require.ErrorAs(t, err, &fe)
			require.Equal(t, ticket.FilterMissingScope, fe.Kind)
			require.Equal(t, refused, fe.Msg)

			// tickets:write implies tickets:read and users:read exists; neither
			// is the search scope.
			for _, other := range []string{"tickets:write", "users:read"} {
				_, err = listFilterFor(actorWith(role, true, other), ticket.VisibilityAll, args)
				require.ErrorAs(t, err, &fe, other)
				require.Equal(t, ticket.FilterMissingScope, fe.Kind, other)
			}

			f, err := listFilterFor(actorWith(role, true, "tickets:read", "requester_search:read"), ticket.VisibilityAll, args)
			require.NoError(t, err)
			require.Equal(t, "whitfield", f.Requester)

			f, err = listFilterFor(actorWith(role, false), ticket.VisibilityAll, args)
			require.NoError(t, err, "a session is the person, and scopes only narrow a machine credential")
			require.Equal(t, "whitfield", f.Requester)
		})
	}

	t.Run("reporter_id by id needs no search scope", func(t *testing.T) {
		_, err := listFilterFor(actorWith(user.RoleStaff, true, "tickets:read"), ticket.VisibilityAll,
			map[string]any{"reporter_id": uuid.NewString()})
		require.NoError(t, err)
	})
}

// include_internal decides whether a reply search may match an internal note.
// It comes from the role and from nothing else.
func TestListFilterFor_IncludeInternalComesFromTheRole(t *testing.T) {
	cases := []struct {
		role user.Role
		want bool
	}{
		{user.RoleAdmin, true},
		{user.RoleStaff, true},
		{user.RoleUser, false},
		{user.Role("somebody-new"), false},
		{user.Role(""), false},
	}
	for _, tc := range cases {
		t.Run(string(tc.role), func(t *testing.T) {
			f, err := listFilterFor(actorWith(tc.role, false), ticket.VisibilityAll,
				map[string]any{"q": "printer", "search_replies": true})
			require.NoError(t, err)
			require.Equal(t, tc.want, f.IncludeInternal)

			// An argument cannot turn it on.
			f, err = listFilterFor(actorWith(tc.role, false), ticket.VisibilityAll,
				map[string]any{"q": "printer", "search_replies": true, "include_internal": true})
			require.NoError(t, err)
			require.Equal(t, tc.want, f.IncludeInternal)
		})
	}
}

func TestListFilterFor_KeepsServerFieldsAndTheLimit(t *testing.T) {
	a := actorWith(user.RoleStaff, false)
	f, err := listFilterFor(a, ticket.VisibilityScoped, map[string]any{"limit": 500.0, "offset": 40.0})
	require.NoError(t, err)
	require.Equal(t, a.UserID, f.ActorID)
	require.Equal(t, ticket.VisibilityScoped, f.Visibility)
	require.Equal(t, ticket.QueueAny, f.Queue)
	require.Equal(t, maxListLimit, f.Limit)
	require.Equal(t, 40, f.Offset)

	f, err = listFilterFor(a, ticket.VisibilityAll, map[string]any{})
	require.NoError(t, err)
	require.Equal(t, 20, f.Limit)
}

// An unknown sort or order is refused for every role, before anything else.
func TestListFilterFor_UnknownSortOrOrderIsRefused(t *testing.T) {
	for _, role := range []user.Role{user.RoleAdmin, user.RoleStaff, user.RoleUser} {
		for _, args := range []map[string]any{
			{"sort": "bogus"}, {"sort": "relevance"}, {"sort": "number", "order": "sideways"}, {"order": "sideways"},
		} {
			_, err := listFilterFor(actorWith(role, false), ticket.VisibilityAll, args)
			require.ErrorIs(t, err, ticket.ErrValidation, "%s %v", role, args)
		}
	}
}

// The handler's refusals happen before the store is reached (s.tickets is nil,
// so reaching it would panic), and a caller's mistake is not an error in the log.
func TestHandleListTickets_RefusalsAreAnsweredAndNotLoggedAsErrors(t *testing.T) {
	s := &Server{authz: &recordingAuthorizer{visibility: ticket.VisibilityAll}}

	cases := []struct {
		name  string
		actor *authmw.Actor
		args  map[string]any
		want  string
	}{
		{"unknown sort", actorWith(user.RoleStaff, false), map[string]any{"sort": "bogus"}, "invalid sort"},
		{"unknown order", actorWith(user.RoleStaff, false), map[string]any{"order": "up"}, "invalid order"},
		{"requester text without the scope", actorWith(user.RoleStaff, true, "tickets:read"),
			map[string]any{"requester": "whitfield"}, "requester_search:read"},
		{"a requester using a staff-only filter", actorWith(user.RoleUser, false),
			map[string]any{"updated_after": "2026-03-01T00:00:00Z"}, "not available to requesters"},
		{"a requester sorting by a field they are not sent", actorWith(user.RoleUser, false),
			map[string]any{"sort": "sla"}, "not available to requesters"},
		{"sla while tracking is not known to be on", actorWith(user.RoleStaff, false),
			map[string]any{"sla": "breached"}, "SLA tracking is off"},
		{"sorting by sla while tracking is not known to be on", actorWith(user.RoleStaff, false),
			map[string]any{"sort": "sla"}, "SLA tracking is off"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(prev) })

			ctx := context.WithValue(context.Background(), actorCtxKey{}, tc.actor)
			res, err := s.handleListTickets(ctx, callToolRequest("list_tickets", tc.args))
			require.NoError(t, err)
			require.True(t, res.IsError)
			require.Contains(t, resultText(t, res), tc.want)
			require.NotContains(t, resultText(t, res), "list tickets failed")
			require.NotContains(t, logs.String(), "level=ERROR")
		})
	}
}

// SLA tracking being off is told to the caller, as REST does, so an empty list
// is never mistaken for "nothing is breached". It is read from the setting on
// every call, and a setting that cannot be read is a server fault.
func TestHandleListTickets_SLAFollowsTheSetting(t *testing.T) {
	ctx := context.WithValue(context.Background(), actorCtxKey{}, actorWith(user.RoleStaff, false))
	args := map[string]any{"sla": "at_risk"}

	off := (&Server{authz: &recordingAuthorizer{visibility: ticket.VisibilityAll}}).
		WithSLAEnabled(func(context.Context) (bool, error) { return false, nil })
	res, err := off.handleListTickets(ctx, callToolRequest("list_tickets", args))
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, resultText(t, res), "SLA tracking is off")

	broken := (&Server{authz: &recordingAuthorizer{visibility: ticket.VisibilityAll}}).
		WithSLAEnabled(func(context.Context) (bool, error) { return false, errors.New("db down") })
	res, err = broken.handleListTickets(ctx, callToolRequest("list_tickets", args))
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Equal(t, "list tickets failed", resultText(t, res), "the database's words are not the caller's")
}

func TestToolSchema_ListTicketsAdvertisesTheFilters(t *testing.T) {
	s := New(nil, nil, nil, nil, nil, nil)
	tool := s.mcp.GetTool("list_tickets")
	require.NotNil(t, tool)
	props := tool.Tool.InputSchema.Properties

	for _, name := range []string{
		"assignee_user_id", "status_id", "priority", "category_id", "q", "search_replies", "limit", "offset", // as before
		"status_ids", "reporter_id", "requester", "created_after", "created_before", "updated_after", "updated_before",
		"type_id", "item_id", "assignee_group_id", "include_closed", "source_channel", "sla", "sort", "order",
	} {
		require.Contains(t, props, name)
	}
	require.NotContains(t, props, "include_internal", "it is not an argument")

	for _, name := range []string{"status_ids", "source_channel"} {
		p, ok := props[name].(map[string]any)
		require.True(t, ok, name)
		require.Equal(t, "array", p["type"], "%s is a JSON array", name)
	}
	// priority is one string for existing callers or a list, and the schema says
	// so: a client that validates against it must be able to send either.
	oneOf, ok := props["priority"].(map[string]any)["oneOf"].([]any)
	require.True(t, ok, "priority declares both shapes")
	var types []string
	for _, alt := range oneOf {
		types = append(types, alt.(map[string]any)["type"].(string))
	}
	require.ElementsMatch(t, []string{"string", "array"}, types)
}
