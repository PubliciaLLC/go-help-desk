package ticket_test

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// What a requester is shown of a ticket is an ALLOWED LIST, applied to the
// response on its way out (Erik, 2026-10-15). These tests pin it from both
// ends: a ticket with every field filled loses everything not on the list, and
// every key a Ticket can produce is classified on purpose.

// filledTicket is a ticket with every exported field set to something non-zero,
// found by reflection so a field added later is filled too.
func filledTicket(t *testing.T) ticket.Ticket {
	t.Helper()
	var tk ticket.Ticket
	fill(reflect.ValueOf(&tk).Elem())
	return tk
}

func fill(v reflect.Value) {
	switch {
	case v.Type() == reflect.TypeOf(time.Time{}):
		v.Set(reflect.ValueOf(time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)))
	case v.Kind() == reflect.Ptr:
		v.Set(reflect.New(v.Type().Elem()))
		fill(v.Elem())
	case v.Kind() == reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				fill(v.Field(i))
			}
		}
	case v.Kind() == reflect.String:
		v.SetString("x")
	case v.Kind() == reflect.Bool:
		v.SetBool(true)
	case v.Kind() >= reflect.Int && v.Kind() <= reflect.Int64:
		v.SetInt(7)
	case v.Kind() == reflect.Array && v.Type().Elem().Kind() == reflect.Uint8: // uuid.UUID
		for i := 0; i < v.Len(); i++ {
			v.Index(i).SetUint(uint64(i + 1))
		}
	}
}

func keysOf(t *testing.T, raw any) map[string]bool {
	t.Helper()
	b, err := json.Marshal(raw)
	require.NoError(t, err)
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &m))
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

// jsonKeysOfType is every key a struct can put on the wire, read from its json
// tags, not from a marshalled value: a field of any type, omitempty or not,
// counts whether or not a value would make it appear. A tag of "-" is never on
// the wire.
func jsonKeysOfType(typ reflect.Type) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		out[name] = true
	}
	return out
}

func sorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func asSet(keys []string) map[string]bool {
	m := map[string]bool{}
	for _, k := range keys {
		m[k] = true
	}
	return m
}

// Every key a Ticket can put on the wire is either shown to requesters or
// explicitly never shown to them. A new field fails here until someone decides.
func TestRequesterKeys_EveryTicketKeyIsClassified(t *testing.T) {
	allowed, never := asSet(ticket.RequesterTicketKeys()), asSet(ticket.RequesterNeverTicketKeys())
	for k := range allowed {
		require.False(t, never[k], "%q is on both lists", k)
	}
	produced := jsonKeysOfType(reflect.TypeOf(ticket.Ticket{}))
	for k := range produced {
		require.True(t, allowed[k] || never[k],
			"Ticket produces the JSON key %q, which is on neither the requester allowed list nor the never list: decide which (requester_view.go)", k)
	}
	for k := range allowed {
		require.True(t, produced[k], "%q is on the allowed list but no Ticket field produces it", k)
	}
	for k := range never {
		require.True(t, produced[k], "%q is on the never list but no Ticket field produces it", k)
	}
	// Spelled out so a change to either list is a visible diff in review.
	require.Equal(t, []string{
		"assignee_user_id", "category_id", "created_at", "description", "id", "item_id", "priority",
		"resolution_notes", "resolved_at", "status_id", "subject", "tracking_number", "type_id",
	}, sorted(allowed))
	require.Equal(t, []string{
		"assignee_group_id", "closed_at", "guest_email", "guest_name", "guest_phone",
		"pending_since", "reopen_count", "reporter_user_id", "sla_paused_seconds", "updated_at",
	}, sorted(never))
}

// Each response shape a ticket leaves the server in: one ticket, a list, and a
// ticket wrapped with extra fields. Only allowed keys survive, and every
// allowed key does.
func TestFilterForViewer_RequesterGetsOnlyTheAllowedList(t *testing.T) {
	tk := filledTicket(t)
	type view struct {
		ticket.Ticket
		SLA       any    `json:"sla"`
		CanReopen bool   `json:"can_reopen"`
		Replies   []any  `json:"replies,omitempty"`
		Links     []any  `json:"links,omitempty"`
		Extra     string `json:"surprise,omitempty"`
	}
	wrapped := view{Ticket: tk, Replies: []any{map[string]any{"id": "r", "body": "hi"}}, Links: []any{map[string]any{"source_id": "a"}}, Extra: "x"}

	allowed := asSet(append(ticket.RequesterTicketKeys(), ticket.RequesterWrapperKeys()...))
	ticketAllowed := asSet(ticket.RequesterTicketKeys())

	for _, role := range []user.Role{user.RoleUser, user.Role("auditor"), user.Role("")} {
		t.Run("one ticket/"+string(role), func(t *testing.T) {
			got, err := ticket.FilterForViewer(role, tk)
			require.NoError(t, err)
			require.Equal(t, sorted(ticketAllowed), sorted(keysOf(t, got)), "exactly the allowed keys, none missing")
		})
		t.Run("list/"+string(role), func(t *testing.T) {
			got, err := ticket.FilterForViewer(role, []ticket.Ticket{tk, tk})
			require.NoError(t, err)
			b, _ := json.Marshal(got)
			var rows []map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(b, &rows))
			require.Len(t, rows, 2)
			for _, row := range rows {
				for k := range row {
					require.True(t, ticketAllowed[k], "list row key %q", k)
				}
				require.Len(t, row, len(ticketAllowed))
			}
		})
		t.Run("wrapped/"+string(role), func(t *testing.T) {
			got, err := ticket.FilterForViewer(role, wrapped)
			require.NoError(t, err)
			keys := keysOf(t, got)
			for k := range keys {
				require.True(t, allowed[k], "wrapped key %q survived", k)
			}
			for k := range ticketAllowed {
				require.True(t, keys[k], "allowed key %q missing", k)
			}
			for _, k := range ticket.RequesterWrapperKeys() {
				require.True(t, keys[k], "wrapper key %q is kept when present", k)
			}
			require.NotContains(t, keys, "surprise", "an unknown wrapper field is hidden until added on purpose")
		})
	}
}

// Staff and administrators get the response exactly as it was built.
func TestFilterForViewer_StaffAndAdminAreUntouched(t *testing.T) {
	tk := filledTicket(t)
	for _, role := range []user.Role{user.RoleStaff, user.RoleAdmin} {
		got, err := ticket.FilterForViewer(role, tk)
		require.NoError(t, err)
		require.Equal(t, keysOf(t, tk), keysOf(t, got), role)
		require.Contains(t, keysOf(t, got), "sla_paused_seconds")
		require.Contains(t, keysOf(t, got), "reopen_count")
	}
}

// Values are not rewritten, only keys dropped; and nothing else is lost.
func TestFilterForViewer_KeepsValuesAndNonTicketPayloadsAlone(t *testing.T) {
	id := uuid.New()
	got, err := ticket.FilterForViewer(user.RoleUser, ticket.Ticket{ID: id, Subject: "s", Priority: ticket.PriorityHigh, ReopenCount: 3})
	require.NoError(t, err)
	b, _ := json.Marshal(got)
	require.Contains(t, string(b), id.String())
	require.Contains(t, string(b), `"priority":"high"`)
	require.NotContains(t, string(b), "reopen_count")

	// Not an object or array: passed through.
	got, err = ticket.FilterForViewer(user.RoleUser, nil)
	require.NoError(t, err)
	require.Nil(t, got)
}

// A guest is shown less than an account holder: a subset of the same list, plus
// the derived status name and the replies.
func TestFilterForGuest_IsASubsetOfTheRequesterList(t *testing.T) {
	requester := asSet(ticket.RequesterTicketKeys())
	for _, k := range ticket.GuestTicketKeys() {
		require.True(t, requester[k], "guest key %q is not on the requester list", k)
	}
	require.Less(t, len(ticket.GuestTicketKeys()), len(requester), "a strict subset")
	in := map[string]any{
		"tracking_number": "T-1", "subject": "s", "description": "d", "status": "New",
		"created_at": "c", "updated_at": "u", "replies": []any{},
		"assignee_user_id": "x", "resolution_notes": "n", "id": "i", "reopen_count": 1,
	}
	got, err := ticket.FilterForGuest(in)
	require.NoError(t, err)
	keys := keysOf(t, got)
	// No updated_at: a guest's list is a strict subset of the requester's, which
	// has none either.
	require.Equal(t, []string{"created_at", "description", "replies", "status", "subject", "tracking_number"}, sorted(keys))
}

// Numbers inside a response pass through the filter exactly. Decoding into
// float64 rounds an integer above 2^53, which would silently corrupt a value
// nested in a kept key (a reply, say); the filter decodes with UseNumber.
func TestFilterForViewer_BigIntegersSurviveUnchanged(t *testing.T) {
	const big = int64(9007199254740993) // 2^53 + 1: not representable as a float64
	in := map[string]any{
		"id":      "x",
		"replies": []any{map[string]any{"n": big}},
		"nested":  "dropped",
	}
	got, err := ticket.FilterForViewer(user.RoleUser, in)
	require.NoError(t, err)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	require.Contains(t, string(b), "9007199254740993")
	require.NotContains(t, string(b), "nested")

	got, err = ticket.FilterForGuest(map[string]any{"subject": "s", "replies": []any{map[string]any{"n": big}}})
	require.NoError(t, err)
	b, _ = json.Marshal(got)
	require.Contains(t, string(b), "9007199254740993")
}
