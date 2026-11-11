package ticket

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

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

// Queue narrows a listing to a work queue. It is set by the handler from the
// `scope` parameter and the caller's role, never parsed from a request, and it
// is ANDed with every other criterion. The zero value is every visible ticket,
// which is what callers that predate it (MCP) already get.
type Queue int

const (
	QueueAny Queue = iota
	// QueueMine is tickets assigned to ActorID or to a group ActorID is in.
	QueueMine
	// QueueUnassigned is tickets with neither an assignee nor a group.
	QueueUnassigned
)

// SLAState is the SLA filter: the buckets the ticket list's indicator shows
// in red and amber. A ticket that is neither is simply not matched.
type SLAState string

const (
	SLABreached SLAState = "breached"
	SLAAtRisk   SLAState = "at_risk"
)

func (s SLAState) valid() bool { return s == SLABreached || s == SLAAtRisk }

// SortKey is a column of the ticket list. Every column the list shows can be
// sorted, and only those.
//
// What each one orders by (the store implements exactly this):
//
//	number    tracking_number, as text. The year and the zero-padded sequence
//	          make that the creation order within a prefix.
//	subject   lower(subject).
//	status    the status's sort_order (the order administrators arranged them
//	          in), then its name.
//	priority  critical, high, medium, low. Ascending is most urgent first.
//	sla       urgency: breached, then at risk, then everything else (on track,
//	          no SLA, tracking off). Ascending is most urgent first.
//	created   created_at.
//	updated   updated_at.
//
// Every sort ends with created_at DESC, id, so that equal rows keep a stable
// order across pages.
type SortKey string

const (
	SortNumber   SortKey = "number"
	SortSubject  SortKey = "subject"
	SortStatus   SortKey = "status"
	SortPriority SortKey = "priority"
	SortSLA      SortKey = "sla"
	SortCreated  SortKey = "created"
	SortUpdated  SortKey = "updated"

	// SortRelevance is what a text search sorts by until a column is chosen.
	// It is never accepted from a request: ResolvedSort produces it, and
	// removing the sort parameter is how a caller gets back to it.
	SortRelevance SortKey = "relevance"
)

// SortOrder is the direction of a sort.
type SortOrder string

const (
	OrderAsc  SortOrder = "asc"
	OrderDesc SortOrder = "desc"
)

type sortColumn struct {
	key          SortKey
	ticketKey    string
	defaultOrder SortOrder
	requesterOK  bool
}

// sortColumns is the one list of sortable columns. ticketKey is the Ticket JSON
// key the order reveals, and requesterOK says whether a requester may sort on
// it: ordering by a field a requester is never sent would show them its order.
// A test holds requesterOK to the requester allowed list.
var sortColumns = []sortColumn{
	{SortNumber, "tracking_number", OrderAsc, true},
	{SortSubject, "subject", OrderAsc, true},
	{SortStatus, "status_id", OrderAsc, true},
	{SortPriority, "priority", OrderAsc, true},
	{SortSLA, "pending_since", OrderAsc, false}, // the SLA is null for a requester; pending_since is what it derives from
	{SortCreated, "created_at", OrderDesc, true},
	{SortUpdated, "updated_at", OrderDesc, false},
}

// Filter is the optional criteria a ticket listing may narrow by, and the
// order it comes back in. A nil pointer or empty slice means "not filtered by
// this", which is why they are not zero values: uuid.Nil and "" are
// indistinguishable from absent.
//
// Different criteria are ANDed. The values of one repeatable criterion are
// ORed. Everything is ANDed with Visibility and Queue, so a filter can only
// narrow what the caller may already see.
//
// Request parameters are parsed by ParseListFilter and checked against the
// caller by AllowedFor. The fields marked "server" are never parsed from a
// request.
type Filter struct {
	ActorID    uuid.UUID  // server
	Visibility Visibility // server
	Queue      Queue      // server

	// IncludeInternal says whether a reply search may match internal notes.
	// server: IncludeInternalFor(role). It is never a request parameter.
	IncludeInternal bool

	// Deprecated: use StatusIDs. Read alongside it (AllStatusIDs) until the
	// callers that set it move, then removed in a separate commit.
	StatusID *uuid.UUID
	// Deprecated: use Priorities. See StatusID.
	Priority       *Priority
	CategoryID     *uuid.UUID // `category_id`; also matches every type and item under it
	AssigneeUserID *uuid.UUID // `assignee_user_id`

	// Query is `q`: a ticket number prefix or text. Trimmed.
	Query string
	// SearchReplies is `search_replies`: with Query, a ticket also matches
	// when one reply contains every word. No effect without Query.
	SearchReplies bool

	ReporterID *uuid.UUID // `reporter_id`: the tickets one account reported
	// Requester is `requester`: free text, 2 to 254 characters trimmed,
	// matched anywhere in the reporting account's name or email and the
	// ticket's guest name and email. Needs the requester_search:read scope on
	// a machine credential.
	Requester string

	// Date ranges, RFC 3339. After is inclusive and Before is exclusive.
	CreatedAfter, CreatedBefore *time.Time
	UpdatedAfter, UpdatedBefore *time.Time

	Priorities      []Priority      // `priority`, repeatable
	TypeID          *uuid.UUID      // `type_id`
	ItemID          *uuid.UUID      // `item_id`
	AssigneeGroupID *uuid.UUID      // `assignee_group_id`
	StatusIDs       []uuid.UUID     // `status_id`, repeatable
	SourceChannels  []SourceChannel // `source_channel`, repeatable
	SLA             SLAState        // `sla`; "" is not filtered

	// ExcludeClosed is `include_closed=false` and no `status_id`: leave out
	// the Closed system status. It is a bool that defaults to false so that
	// the zero Filter includes closed tickets, as every listing always has.
	// The store resolves the status by its kind; the parser does not know ids.
	ExcludeClosed bool

	// Sort is `sort` and Order is `order`. Empty means "default": see
	// ResolvedSort.
	Sort  SortKey
	Order SortOrder

	Limit  int // server
	Offset int // server
}

// AllStatusIDs is StatusIDs plus the deprecated StatusID, never nil: the SQL
// guard is cardinality(), and an absent filter is an empty slice.
func (f Filter) AllStatusIDs() []uuid.UUID {
	out := append([]uuid.UUID{}, f.StatusIDs...)
	if f.StatusID != nil && !slices.Contains(out, *f.StatusID) {
		out = append(out, *f.StatusID)
	}
	return out
}

// AllPriorities is Priorities plus the deprecated Priority, never nil.
func (f Filter) AllPriorities() []Priority {
	out := append([]Priority{}, f.Priorities...)
	if f.Priority != nil && !slices.Contains(out, *f.Priority) {
		out = append(out, *f.Priority)
	}
	return out
}

// ResolvedSort is the sort to apply, with defaults filled in:
//
//   - no sort, no text search: created, newest first (what every listing has
//     always done);
//   - no sort, text search: relevance, best match first. This is the only
//     time relevance applies. Choosing any column replaces it: the text still
//     narrows the results but no longer orders them;
//   - a sort without an order: that column's natural order (dates newest
//     first, everything else first-to-last or most urgent first);
//   - an order without a sort is ignored.
func (f Filter) ResolvedSort() (SortKey, SortOrder) {
	if f.Sort == "" {
		if strings.TrimSpace(f.Query) != "" {
			return SortRelevance, OrderDesc
		}
		return SortCreated, OrderDesc
	}
	for _, c := range sortColumns {
		if c.key == f.Sort {
			if f.Order != "" {
				return c.key, f.Order
			}
			return c.key, c.defaultOrder
		}
	}
	return f.Sort, f.Order // not reachable for a filter that came from ParseListFilter
}

// IncludeInternalFor says whether a role's reply search may match internal
// notes. Only staff and administrators may; anyone else, including a role this
// does not recognise, searches public replies only. Fails closed.
func IncludeInternalFor(role user.Role) bool {
	return role == user.RoleStaff || role == user.RoleAdmin
}

// FilterErrorKind says which answer a refused request gets.
type FilterErrorKind int

const (
	// FilterInvalid: 400 bad_request.
	FilterInvalid FilterErrorKind = iota + 1
	// FilterForbidden: 403 forbidden.
	FilterForbidden
	// FilterMissingScope: 403 insufficient_scope.
	FilterMissingScope
)

// FilterError is a refused listing request. Its text is the wording the API
// answers with, so REST and MCP say the same thing.
type FilterError struct {
	Kind FilterErrorKind
	Msg  string
}

func (e *FilterError) Error() string { return e.Msg }

// Is makes a FilterInvalid an ErrValidation, which MCP already reports.
func (e *FilterError) Is(target error) bool {
	return target == ErrValidation && e.Kind == FilterInvalid
}

func invalid(format string, args ...any) *FilterError {
	return &FilterError{Kind: FilterInvalid, Msg: fmt.Sprintf(format, args...)}
}

// maxRepeated is the most values one repeatable key may carry.
const maxRepeated = 20

// ParseListFilter reads the filter and sort parameters of GET /tickets. It does
// no authorisation (see AllowedFor) and sets none of the server fields.
//
// An empty value is the same as an absent one. A key sent twice where one value
// is expected uses the first. Unknown parameters, `include_internal` among
// them, are ignored. The `scope`, `limit` and `offset` parameters belong to the
// handler and are not read here.
func ParseListFilter(q url.Values) (Filter, error) {
	var (
		f   Filter
		err error
	)
	f.Query = strings.TrimSpace(q.Get("q"))

	if f.SearchReplies, err = parseBool(q, "search_replies", false); err != nil {
		return Filter{}, err
	}
	includeClosed, err := parseBool(q, "include_closed", true)
	if err != nil {
		return Filter{}, err
	}

	for _, p := range []struct {
		key string
		dst **uuid.UUID
	}{
		{"reporter_id", &f.ReporterID},
		{"category_id", &f.CategoryID},
		{"type_id", &f.TypeID},
		{"item_id", &f.ItemID},
		{"assignee_user_id", &f.AssigneeUserID},
		{"assignee_group_id", &f.AssigneeGroupID},
	} {
		if *p.dst, err = parseUUID(q.Get(p.key), p.key); err != nil {
			return Filter{}, err
		}
	}

	for _, p := range []struct {
		key string
		dst **time.Time
	}{
		{"created_after", &f.CreatedAfter},
		{"created_before", &f.CreatedBefore},
		{"updated_after", &f.UpdatedAfter},
		{"updated_before", &f.UpdatedBefore},
	} {
		if *p.dst, err = parseInstant(q.Get(p.key), p.key); err != nil {
			return Filter{}, err
		}
	}
	if !rangeOK(f.CreatedAfter, f.CreatedBefore) {
		return Filter{}, invalid("created_after must be before created_before")
	}
	if !rangeOK(f.UpdatedAfter, f.UpdatedBefore) {
		return Filter{}, invalid("updated_after must be before updated_before")
	}

	if f.StatusIDs, err = parseRepeated(q, "status_id", func(s string) (uuid.UUID, bool) {
		id, err := uuid.Parse(s)
		return id, err == nil
	}); err != nil {
		return Filter{}, err
	}
	if f.Priorities, err = parseRepeated(q, "priority", func(s string) (Priority, bool) {
		return Priority(s), Priority(s).Valid()
	}); err != nil {
		return Filter{}, err
	}
	if f.SourceChannels, err = parseRepeated(q, "source_channel", func(s string) (SourceChannel, bool) {
		return SourceChannel(s), SourceChannel(s).Valid()
	}); err != nil {
		return Filter{}, err
	}

	if s := SLAState(q.Get("sla")); s != "" {
		if !s.valid() {
			return Filter{}, invalid("invalid sla")
		}
		f.SLA = s
	}

	// An explicit status wins over the toggle: asking for Closed by name and
	// being told it is excluded would be absurd.
	f.ExcludeClosed = !includeClosed && len(f.StatusIDs) == 0

	if r := strings.TrimSpace(q.Get("requester")); r != "" {
		if n := utf8.RuneCountInString(r); n < 2 || n > 254 {
			return Filter{}, invalid("requester must be 2 to 254 characters")
		}
		f.Requester = r
	}

	if s := SortKey(q.Get("sort")); s != "" {
		if !knownSort(s) {
			return Filter{}, invalid("invalid sort")
		}
		f.Sort = s
	}
	// Checked even without a sort, so a typo is not silently ignored.
	if o := SortOrder(q.Get("order")); o != "" {
		if o != OrderAsc && o != OrderDesc {
			return Filter{}, invalid("invalid order")
		}
		f.Order = o
	}
	return f, nil
}

func knownSort(s SortKey) bool {
	return slices.ContainsFunc(sortColumns, func(c sortColumn) bool { return c.key == s })
}

func parseBool(q url.Values, key string, def bool) (bool, error) {
	switch q.Get(key) {
	case "":
		return def, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, invalid("invalid %s", key)
}

func parseUUID(v, key string) (*uuid.UUID, error) {
	if v == "" {
		return nil, nil
	}
	id, err := uuid.Parse(v)
	if err != nil {
		return nil, invalid("invalid %s", key)
	}
	return &id, nil
}

func parseInstant(v, key string) (*time.Time, error) {
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, invalid("invalid %s", key)
	}
	return &t, nil
}

// rangeOK reports whether a range is open at either end or runs forward.
func rangeOK(after, before *time.Time) bool {
	return after == nil || before == nil || after.Before(*before)
}

// parseRepeated reads a repeatable key. Empty values are skipped and do not
// count toward the limit. It returns nil, not an empty slice, when absent.
func parseRepeated[T any](q url.Values, key string, conv func(string) (T, bool)) ([]T, error) {
	var out []T
	for _, raw := range q[key] {
		if raw == "" {
			continue
		}
		if len(out) == maxRepeated {
			return nil, invalid("too many %s values (max %d)", key, maxRepeated)
		}
		v, ok := conv(raw)
		if !ok {
			return nil, invalid("invalid %s", key)
		}
		out = append(out, v)
	}
	return out, nil
}

// requesterRules says, for each filter a request can set, which parameter it is,
// which Ticket JSON key it reads, and whether a requester may use it.
//
// A requester may filter only on what they are sent (requester_view.go). If
// they could filter on updated_at, narrowing the range step by step would read
// the minute staff last touched a ticket. requesterOK is therefore never true
// for a key off the allowed list (a test holds it to that), and may be false for
// one on it: assignee_user_id is sent to requesters, and the owner still keeps
// the filter to staff.
var requesterRules = []struct {
	field       string
	param       string
	ticketKey   string
	requesterOK bool
	set         func(Filter) bool
}{
	{"ReporterID", "reporter_id", "reporter_user_id", false, func(f Filter) bool { return f.ReporterID != nil }},
	{"Requester", "requester", "reporter_user_id", false, func(f Filter) bool { return f.Requester != "" }},
	{"CreatedAfter", "created_after", "created_at", true, func(f Filter) bool { return f.CreatedAfter != nil }},
	{"CreatedBefore", "created_before", "created_at", true, func(f Filter) bool { return f.CreatedBefore != nil }},
	{"UpdatedAfter", "updated_after", "updated_at", false, func(f Filter) bool { return f.UpdatedAfter != nil }},
	{"UpdatedBefore", "updated_before", "updated_at", false, func(f Filter) bool { return f.UpdatedBefore != nil }},
	{"Priorities", "priority", "priority", true, func(f Filter) bool { return len(f.Priorities) > 0 }},
	{"CategoryID", "category_id", "category_id", true, func(f Filter) bool { return f.CategoryID != nil }},
	{"TypeID", "type_id", "type_id", true, func(f Filter) bool { return f.TypeID != nil }},
	{"ItemID", "item_id", "item_id", true, func(f Filter) bool { return f.ItemID != nil }},
	{"AssigneeUserID", "assignee_user_id", "assignee_user_id", false, func(f Filter) bool { return f.AssigneeUserID != nil }},
	{"AssigneeGroupID", "assignee_group_id", "assignee_group_id", false, func(f Filter) bool { return f.AssigneeGroupID != nil }},
	{"StatusIDs", "status_id", "status_id", true, func(f Filter) bool { return len(f.StatusIDs) > 0 }},
	{"ExcludeClosed", "include_closed", "status_id", true, func(f Filter) bool { return f.ExcludeClosed }},
	{"SourceChannels", "source_channel", "source_channel", false, func(f Filter) bool { return len(f.SourceChannels) > 0 }},
	{"SLA", "sla", "pending_since", false, func(f Filter) bool { return f.SLA != "" }}, // the SLA is null for a requester; pending_since is what it derives from
}

// AllowedFor checks the filter against who is asking. role is the caller's role
// (a key or OAuth client has its owner's or service user's role).
// requesterSearch says whether the caller may send free-text `requester`: true
// for a browser session, and for an API key or OAuth client only if it holds
// requester_search:read. The caller works that out, because the scope lives with
// the credential, not with the ticket.
//
// An unknown sort or order is a FilterInvalid for every role.
//
// A requester, or any role this does not recognise, may use only the filters
// and sorts marked requesterOK: 403 forbidden. Staff and administrators may
// use all of them, except that `requester` without requesterSearch is 403
// insufficient_scope. The one existing refusal not made here is the
// assignee_group_id membership check under scope enforcement, which needs the
// caller's groups.
func (f Filter) AllowedFor(role user.Role, requesterSearch bool) error {
	// Fail closed on a sort or order ParseListFilter would have refused: MCP
	// builds a Filter from a map and may not have gone through it. Before the
	// role checks, so the answer is the same for everyone.
	if f.Sort != "" && !knownSort(f.Sort) {
		return invalid("invalid sort")
	}
	if f.Order != "" && f.Order != OrderAsc && f.Order != OrderDesc {
		return invalid("invalid order")
	}
	if role == user.RoleStaff || role == user.RoleAdmin {
		if f.Requester != "" && !requesterSearch {
			return &FilterError{Kind: FilterMissingScope, Msg: "this credential does not carry the requester_search:read scope"}
		}
		return nil
	}
	for _, r := range requesterRules {
		if r.set(f) && !r.requesterOK {
			return &FilterError{Kind: FilterForbidden, Msg: r.param + " is not available to requesters"}
		}
	}
	for _, c := range sortColumns {
		if c.key == f.Sort && !c.requesterOK {
			return &FilterError{Kind: FilterForbidden, Msg: "sort by " + string(c.key) + " is not available to requesters"}
		}
	}
	return nil
}
