package ticket

import (
	"errors"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

func mustParse(t *testing.T, raw string) Filter {
	t.Helper()
	q, err := url.ParseQuery(raw)
	require.NoError(t, err)
	f, err := ParseListFilter(q)
	require.NoError(t, err)
	return f
}

func parseErr(t *testing.T, raw string) *FilterError {
	t.Helper()
	q, err := url.ParseQuery(raw)
	require.NoError(t, err)
	_, err = ParseListFilter(q)
	var fe *FilterError
	require.ErrorAs(t, err, &fe)
	return fe
}

func TestParseListFilter_Valid(t *testing.T) {
	id := uuid.New()
	id2 := uuid.New()
	ts := time.Date(2026, 11, 1, 9, 30, 0, 0, time.UTC)

	tests := []struct {
		name string
		raw  string
		want func(Filter) bool
	}{
		{"nothing", "", func(f Filter) bool { return reflect.DeepEqual(f, Filter{}) }},
		{"q is trimmed", "q=+printer+", func(f Filter) bool { return f.Query == "printer" }},
		{"search_replies", "search_replies=true", func(f Filter) bool { return f.SearchReplies }},
		{"reporter_id", "reporter_id=" + id.String(), func(f Filter) bool { return *f.ReporterID == id }},
		{"requester trimmed", "requester=+Dana+", func(f Filter) bool { return f.Requester == "Dana" }},
		{"created_after", "created_after=2026-11-01T09:30:00Z", func(f Filter) bool { return f.CreatedAfter.Equal(ts) }},
		{"created_before offset", "created_before=2026-11-01T10:30:00%2B01:00", func(f Filter) bool { return f.CreatedBefore.Equal(ts) }},
		{"updated range", "updated_after=2026-11-01T00:00:00Z&updated_before=2026-11-02T00:00:00Z", func(f Filter) bool {
			return f.UpdatedAfter != nil && f.UpdatedBefore != nil
		}},
		{"priority repeated", "priority=high&priority=low", func(f Filter) bool {
			return slices.Equal(f.Priorities, []Priority{PriorityHigh, PriorityLow})
		}},
		{"category type item", "category_id=" + id.String() + "&type_id=" + id2.String() + "&item_id=" + id.String(), func(f Filter) bool {
			return *f.CategoryID == id && *f.TypeID == id2 && *f.ItemID == id
		}},
		{"assignees", "assignee_user_id=" + id.String() + "&assignee_group_id=" + id2.String(), func(f Filter) bool {
			return *f.AssigneeUserID == id && *f.AssigneeGroupID == id2
		}},
		{"status_id repeated", "status_id=" + id.String() + "&status_id=" + id2.String(), func(f Filter) bool {
			return slices.Equal(f.StatusIDs, []uuid.UUID{id, id2})
		}},
		{"source_channel repeated", "source_channel=web&source_channel=phone", func(f Filter) bool {
			return slices.Equal(f.SourceChannels, []SourceChannel{ChannelWeb, ChannelPhone})
		}},
		{"sla", "sla=at_risk", func(f Filter) bool { return f.SLA == SLAAtRisk }},
		{"empty values are absent", "q=&reporter_id=&status_id=&priority=&sla=&sort=&order=&created_after=", func(f Filter) bool {
			return reflect.DeepEqual(f, Filter{})
		}},
		{"unknown parameters are ignored", "colour=red&scope=mine&limit=5", func(f Filter) bool { return reflect.DeepEqual(f, Filter{}) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.True(t, tc.want(mustParse(t, tc.raw)), "%+v", mustParse(t, tc.raw))
		})
	}
}

func TestParseListFilter_Invalid(t *testing.T) {
	id := uuid.New().String()
	tests := []struct {
		name string
		raw  string
		msg  string
	}{
		{"reporter_id", "reporter_id=nope", "invalid reporter_id"},
		{"category_id", "category_id=nope", "invalid category_id"},
		{"type_id", "type_id=nope", "invalid type_id"},
		{"item_id", "item_id=nope", "invalid item_id"},
		{"assignee_user_id", "assignee_user_id=nope", "invalid assignee_user_id"},
		{"assignee_group_id", "assignee_group_id=nope", "invalid assignee_group_id"},
		{"status_id", "status_id=" + id + "&status_id=nope", "invalid status_id"},
		{"priority", "priority=urgent", "invalid priority"},
		{"source_channel", "source_channel=fax", "invalid source_channel"},
		{"sla", "sla=ok", "invalid sla"},
		{"created_after", "created_after=yesterday", "invalid created_after"},
		{"date only is not RFC 3339", "created_before=2026-11-01", "invalid created_before"},
		{"updated_after", "updated_after=x", "invalid updated_after"},
		{"updated_before", "updated_before=x", "invalid updated_before"},
		{"search_replies", "search_replies=maybe", "invalid search_replies"},
		{"include_closed", "include_closed=1", "invalid include_closed"},
		{"include_closed checked even when status_id wins", "include_closed=x&status_id=" + id, "invalid include_closed"},
		{"sort", "sort=relevance", "invalid sort"},
		{"sort unknown", "sort=colour", "invalid sort"},
		{"order", "sort=created&order=up", "invalid order"},
		{"order checked without sort", "order=up", "invalid order"},
		{"created range inverted", "created_after=2026-11-02T00:00:00Z&created_before=2026-11-01T00:00:00Z", "created_after must be before created_before"},
		{"created range empty", "created_after=2026-11-01T00:00:00Z&created_before=2026-11-01T00:00:00Z", "created_after must be before created_before"},
		{"updated range inverted", "updated_after=2026-11-02T00:00:00Z&updated_before=2026-11-01T00:00:00Z", "updated_after must be before updated_before"},
		{"requester too short", "requester=a", "requester must be 2 to 254 characters"},
		{"requester short after trim", "requester=+a+", "requester must be 2 to 254 characters"},
		{"requester too long", "requester=" + strings.Repeat("x", 255), "requester must be 2 to 254 characters"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fe := parseErr(t, tc.raw)
			require.Equal(t, FilterInvalid, fe.Kind)
			require.Equal(t, tc.msg, fe.Error())
			require.True(t, errors.Is(fe, ErrValidation), "an invalid filter is an ErrValidation for MCP")
		})
	}
}

func TestParseListFilter_RequesterLengthIsCharacters(t *testing.T) {
	// 254 multi-byte characters are well over 254 bytes.
	require.Equal(t, strings.Repeat("é", 254), mustParse(t, "requester="+strings.Repeat("é", 254)).Requester)
	require.Equal(t, "éé", mustParse(t, "requester=éé").Requester)
}

func TestParseListFilter_RepeatedKeyLimit(t *testing.T) {
	keys := map[string]func(int) string{
		"status_id":      func(int) string { return uuid.NewString() },
		"priority":       func(int) string { return "high" },
		"source_channel": func(int) string { return "web" },
	}
	for key, val := range keys {
		t.Run(key, func(t *testing.T) {
			build := func(n int) string {
				q := url.Values{}
				for i := range n {
					q.Add(key, val(i))
				}
				return q.Encode()
			}
			mustParse(t, build(20))
			fe := parseErr(t, build(21))
			require.Equal(t, FilterInvalid, fe.Kind)
			require.Equal(t, "too many "+key+" values (max 20)", fe.Error())
		})
	}
}

func TestParseListFilter_EmptyValuesDoNotCountTowardTheLimit(t *testing.T) {
	q := url.Values{"status_id": append(slices.Repeat([]string{""}, 30), uuid.NewString())}
	f, err := ParseListFilter(q)
	require.NoError(t, err)
	require.Len(t, f.StatusIDs, 1)
}

func TestParseListFilter_IncludeClosed(t *testing.T) {
	id := uuid.NewString()
	tests := []struct {
		raw  string
		want bool // ExcludeClosed
	}{
		{"", false}, // default is to include, as the API always has
		{"include_closed=true", false},
		{"include_closed=false", true},
		{"include_closed=false&status_id=" + id, false}, // an explicit status wins
	}
	for _, tc := range tests {
		require.Equal(t, tc.want, mustParse(t, tc.raw).ExcludeClosed, tc.raw)
	}
}

// The server decides these. A request that names them must not be able to set
// them, which is the point of the contract: filters narrow, they never widen.
func TestParseListFilter_NeverSetsWhatTheServerDecides(t *testing.T) {
	f := mustParse(t, "include_internal=true&actor_id="+uuid.NewString()+"&visibility=all&queue=mine&scope=all&limit=5&offset=5")
	require.Equal(t, Filter{}, f)
}

func TestFilter_ResolvedSort(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantKey   SortKey
		wantOrder SortOrder
	}{
		{"no q, no sort: newest first", "", SortCreated, OrderDesc},
		{"q, no sort: relevance", "q=printer", SortRelevance, OrderDesc},
		{"q of only spaces is not a search", "q=+++", SortCreated, OrderDesc},
		{"q and a column: the column wins", "q=printer&sort=subject", SortSubject, OrderAsc},
		{"q and a column and an order", "q=printer&sort=created&order=asc", SortCreated, OrderAsc},
		{"number", "sort=number", SortNumber, OrderAsc},
		{"status", "sort=status", SortStatus, OrderAsc},
		{"priority: most urgent first", "sort=priority", SortPriority, OrderAsc},
		{"sla: most urgent first", "sort=sla", SortSLA, OrderAsc},
		{"created: newest first", "sort=created", SortCreated, OrderDesc},
		{"updated: newest first", "sort=updated", SortUpdated, OrderDesc},
		{"order alone changes nothing", "order=asc", SortCreated, OrderDesc},
		{"order alone changes nothing with q", "q=x&order=asc", SortRelevance, OrderDesc},
		{"explicit desc", "sort=subject&order=desc", SortSubject, OrderDesc},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			k, o := mustParse(t, tc.raw).ResolvedSort()
			require.Equal(t, tc.wantKey, k)
			require.Equal(t, tc.wantOrder, o)
		})
	}
}

// Every sort value the page can send is accepted, and the list of them is the
// seven visible columns and nothing else.
func TestSortColumns_AreTheVisibleColumns(t *testing.T) {
	var got []string
	for _, c := range sortColumns {
		got = append(got, string(c.key))
		mustParse(t, "sort="+string(c.key))
	}
	require.ElementsMatch(t, []string{"number", "subject", "status", "priority", "sla", "created", "updated"}, got)
}

// ── role and credential ──────────────────────────────────────────────────────

var allRoles = []struct {
	name      string
	role      user.Role
	requester bool // treated as a requester: allowed list only
}{
	{"admin", user.RoleAdmin, false},
	{"staff", user.RoleStaff, false},
	{"user", user.RoleUser, true},
	{"unknown role", user.Role("auditor"), true},
	{"no role", user.Role(""), true},
}

// filterRaw gives one valid query string per filter, so the role tests cover
// each in isolation.
var filterRaw = map[string]string{
	"Query":           "q=x",
	"SearchReplies":   "search_replies=true",
	"ReporterID":      "reporter_id=" + uuid.NewString(),
	"Requester":       "requester=dana",
	"CreatedAfter":    "created_after=2026-11-01T00:00:00Z",
	"CreatedBefore":   "created_before=2026-11-01T00:00:00Z",
	"UpdatedAfter":    "updated_after=2026-11-01T00:00:00Z",
	"UpdatedBefore":   "updated_before=2026-11-01T00:00:00Z",
	"Priorities":      "priority=high",
	"CategoryID":      "category_id=" + uuid.NewString(),
	"TypeID":          "type_id=" + uuid.NewString(),
	"ItemID":          "item_id=" + uuid.NewString(),
	"AssigneeUserID":  "assignee_user_id=" + uuid.NewString(),
	"AssigneeGroupID": "assignee_group_id=" + uuid.NewString(),
	"StatusIDs":       "status_id=" + uuid.NewString(),
	"ExcludeClosed":   "include_closed=false",
	"SourceChannels":  "source_channel=web",
	"SLA":             "sla=breached",
}

// Fields of Filter that a request never sets. Listing them here is how a new
// field gets noticed: it is either on this list or it needs a filterRaw entry
// and a rule in requesterRules.
var serverSetFields = []string{
	"ActorID", "Visibility", "Queue", "IncludeInternal", "Limit", "Offset",
	"StatusID", "Priority", // deprecated singulars, still read by MCP and the store
	"Sort", "Order", // covered by the sort tests
}

func TestFilter_EveryFieldIsClassified(t *testing.T) {
	rules := map[string]bool{}
	for _, r := range requesterRules {
		rules[r.field] = true
	}
	rt := reflect.TypeOf(Filter{})
	for i := range rt.NumField() {
		name := rt.Field(i).Name
		_, inRaw := filterRaw[name]
		require.True(t, slices.Contains(serverSetFields, name) || (inRaw && (rules[name] || name == "Query" || name == "SearchReplies")),
			"Filter.%s is not classified: add it to filterRaw and requesterRules, or to serverSetFields", name)
	}
	for _, r := range requesterRules {
		require.True(t, filterRaw[r.field] != "", "rule for %s has no filterRaw entry", r.field)
	}
}

// A requester may filter only on what a requester is sent. Otherwise a filter
// narrowed step by step would read a field they are never shown, e.g.
// updated_after bisecting to the minute staff last touched a ticket.
func TestRequesterRules_OnlyAllowedListKeys(t *testing.T) {
	allowed := RequesterTicketKeys()
	for _, r := range requesterRules {
		if r.requesterOK {
			require.Contains(t, allowed, r.ticketKey, "%s is open to requesters but %q is not on the allowed list", r.param, r.ticketKey)
		}
	}
	for _, c := range sortColumns {
		if c.requesterOK {
			require.Contains(t, allowed, c.ticketKey, "sort=%s is open to requesters but %q is not on the allowed list", c.key, c.ticketKey)
		}
	}
}

func TestAllowedFor_FilterByRole(t *testing.T) {
	staffOnly := map[string]bool{
		"ReporterID": true, "Requester": true, "UpdatedAfter": true, "UpdatedBefore": true,
		"AssigneeUserID": true, "AssigneeGroupID": true, "SourceChannels": true, "SLA": true,
	}
	for field, raw := range filterRaw {
		for _, r := range allRoles {
			t.Run(field+"/"+r.name, func(t *testing.T) {
				f := mustParse(t, raw)
				err := f.AllowedFor(r.role, true)
				if r.requester && staffOnly[field] {
					var fe *FilterError
					require.ErrorAs(t, err, &fe)
					require.Equal(t, FilterForbidden, fe.Kind)
					require.Equal(t, requesterMessage(field), fe.Error())
					return
				}
				require.NoError(t, err)
			})
		}
	}
}

func requesterMessage(field string) string {
	for _, r := range requesterRules {
		if r.field == field {
			return r.param + " is not available to requesters"
		}
	}
	return ""
}

func TestAllowedFor_RequesterText(t *testing.T) {
	f := mustParse(t, "requester=dana")
	tests := []struct {
		name            string
		role            user.Role
		requesterSearch bool
		want            FilterErrorKind // 0 means no error
	}{
		{"staff session", user.RoleStaff, true, 0},
		{"admin session", user.RoleAdmin, true, 0},
		{"staff key with the scope", user.RoleStaff, true, 0},
		{"staff key without it", user.RoleStaff, false, FilterMissingScope},
		{"admin key without it", user.RoleAdmin, false, FilterMissingScope},
		{"requester session", user.RoleUser, true, FilterForbidden},
		{"requester, even holding the scope", user.RoleUser, true, FilterForbidden},
		{"requester without it is forbidden, not missing scope", user.RoleUser, false, FilterForbidden},
		{"unknown role", user.Role("auditor"), true, FilterForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := f.AllowedFor(tc.role, tc.requesterSearch)
			if tc.want == 0 {
				require.NoError(t, err)
				return
			}
			var fe *FilterError
			require.ErrorAs(t, err, &fe)
			require.Equal(t, tc.want, fe.Kind)
			if tc.want == FilterMissingScope {
				require.Equal(t, "this credential does not carry the requester_search:read scope", fe.Error())
			}
		})
	}
}

// reporter_id and every other filter stay open to a key without the scope.
func TestAllowedFor_OnlyRequesterTextNeedsTheScope(t *testing.T) {
	for field, raw := range filterRaw {
		if field == "Requester" {
			continue
		}
		require.NoError(t, mustParse(t, raw).AllowedFor(user.RoleStaff, false), field)
	}
}

func TestAllowedFor_Sort(t *testing.T) {
	tests := []struct {
		sort      string
		requester bool // requester sees the column
	}{
		{"number", true}, {"subject", true}, {"status", true}, {"priority", true}, {"created", true},
		{"updated", false}, {"sla", false},
	}
	for _, tc := range tests {
		f := mustParse(t, "sort="+tc.sort)
		for _, r := range allRoles {
			err := f.AllowedFor(r.role, true)
			if r.requester && !tc.requester {
				var fe *FilterError
				require.ErrorAs(t, err, &fe, "%s/%s", tc.sort, r.name)
				require.Equal(t, FilterForbidden, fe.Kind)
				require.Equal(t, "sort by "+tc.sort+" is not available to requesters", fe.Error())
				continue
			}
			require.NoError(t, err, "%s/%s", tc.sort, r.name)
		}
	}
}

// The default sort of a text search is relevance, which uses no column.
func TestAllowedFor_DefaultSortIsOpenToRequesters(t *testing.T) {
	require.NoError(t, mustParse(t, "q=printer").AllowedFor(user.RoleUser, false))
}

func TestIncludeInternalFor(t *testing.T) {
	require.True(t, IncludeInternalFor(user.RoleStaff))
	require.True(t, IncludeInternalFor(user.RoleAdmin))
	require.False(t, IncludeInternalFor(user.RoleUser))
	require.False(t, IncludeInternalFor(user.Role("auditor")))
	require.False(t, IncludeInternalFor(user.Role("")))
}

// The deprecated singular fields are still honoured until they are removed, so
// a store reading only the plural ones would silently drop MCP's filter.
func TestFilter_AllStatusIDsAndPriorities(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	hi := PriorityHigh

	empty := Filter{}
	require.NotNil(t, empty.AllStatusIDs(), "absent is an empty slice, not nil: the SQL guard is cardinality()")
	require.Empty(t, empty.AllStatusIDs())
	require.NotNil(t, empty.AllPriorities())
	require.Empty(t, empty.AllPriorities())

	f := Filter{StatusID: &a, StatusIDs: []uuid.UUID{b}, Priority: &hi, Priorities: []Priority{PriorityLow}}
	require.Equal(t, []uuid.UUID{b, a}, f.AllStatusIDs())
	require.Equal(t, []Priority{PriorityLow, PriorityHigh}, f.AllPriorities())

	dup := Filter{StatusID: &a, StatusIDs: []uuid.UUID{a}}
	require.Equal(t, []uuid.UUID{a}, dup.AllStatusIDs())
}

// ParseListFilter rejects an unknown sort, but MCP builds a Filter from a map
// and may never call it. AllowedFor is the check every caller runs, so it must
// refuse a sort or order the store has no ORDER BY for, whatever the role.
func TestAllowedFor_UnknownSortOrOrderIsInvalidForEveryone(t *testing.T) {
	for _, f := range []Filter{
		{Sort: "colour"},
		{Sort: SortRelevance}, // produced by ResolvedSort, never accepted as input
		{Sort: SortCreated, Order: "sideways"},
		{Order: "sideways"},
	} {
		for _, r := range allRoles {
			err := f.AllowedFor(r.role, true)
			var fe *FilterError
			require.ErrorAs(t, err, &fe, "%+v/%s", f, r.name)
			require.Equal(t, FilterInvalid, fe.Kind, "%+v/%s", f, r.name)
		}
	}
	require.EqualError(t, Filter{Sort: "colour"}.AllowedFor(user.RoleStaff, true), "invalid sort")
	require.EqualError(t, Filter{Order: "sideways"}.AllowedFor(user.RoleUser, false), "invalid order")
}

// Only an invalid filter is an ErrValidation (which MCP reports as the caller's
// mistake). A refusal on authority is not one: reporting it as a validation
// error would tell a caller to fix a request that is fine.
func TestFilterError_IsErrValidationOnlyWhenInvalid(t *testing.T) {
	require.True(t, errors.Is(&FilterError{Kind: FilterInvalid}, ErrValidation))
	require.False(t, errors.Is(&FilterError{Kind: FilterForbidden}, ErrValidation))
	require.False(t, errors.Is(&FilterError{Kind: FilterMissingScope}, ErrValidation))

	// And through the functions that produce them.
	require.False(t, errors.Is(Filter{Requester: "dana"}.AllowedFor(user.RoleStaff, false), ErrValidation))
	require.False(t, errors.Is(Filter{Sort: SortUpdated}.AllowedFor(user.RoleUser, false), ErrValidation))
	require.True(t, errors.Is(Filter{Sort: "colour"}.AllowedFor(user.RoleStaff, false), ErrValidation))
}
