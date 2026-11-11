package notification

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestWebhookFilter_Admits pins the whole predicate: the empty filter, each
// condition on its own, the AND across conditions, invert, a deleted value,
// and fail-open when the ticket's attributes are not known (#413).
func TestWebhookFilter_Admits(t *testing.T) {
	catA := uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
	catB := uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000002")
	grpA := uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000001")
	grpB := uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000002")
	critical := &TicketState{CategoryID: catA, Priority: "critical", AssigneeGroupID: &grpA}

	withPriority := func(a *TicketState, p string) *TicketState {
		c := *a
		c.Priority = p
		return &c
	}
	unassigned := func(a *TicketState) *TicketState {
		c := *a
		c.AssigneeGroupID = nil
		return &c
	}

	cases := []struct {
		name      string
		filter    WebhookFilter
		attrs     *TicketState
		wantAdmit bool
		wantKnown bool
	}{
		{"empty filter admits", WebhookFilter{}, critical, true, true},
		{"empty filter admits unknown attrs", WebhookFilter{}, nil, true, true},
		{"empty inverted filter admits (the handler refuses to store it; the predicate stays total)",
			WebhookFilter{Invert: true}, critical, true, true},
		{"category match", WebhookFilter{CategoryIDs: []uuid.UUID{catA}}, critical, true, true},
		{"category miss", WebhookFilter{CategoryIDs: []uuid.UUID{catB}}, critical, false, true},
		{"category any-of", WebhookFilter{CategoryIDs: []uuid.UUID{catB, catA}}, critical, true, true},
		{"priority match", WebhookFilter{Priorities: []string{"critical"}}, critical, true, true},
		{"priority miss", WebhookFilter{Priorities: []string{"low", "high"}}, critical, false, true},
		{"group match", WebhookFilter{GroupIDs: []uuid.UUID{grpA}}, critical, true, true},
		{"group miss", WebhookFilter{GroupIDs: []uuid.UUID{grpB}}, critical, false, true},
		{"unassigned ticket never matches a group",
			WebhookFilter{GroupIDs: []uuid.UUID{grpA}}, unassigned(critical), false, true},
		{"all conditions must hold",
			WebhookFilter{CategoryIDs: []uuid.UUID{catA}, Priorities: []string{"low"}}, critical, false, true},
		{"all conditions hold",
			WebhookFilter{CategoryIDs: []uuid.UUID{catA}, Priorities: []string{"critical"}, GroupIDs: []uuid.UUID{grpA}},
			critical, true, true},
		{"inverted match is refused",
			WebhookFilter{CategoryIDs: []uuid.UUID{catA}, Priorities: []string{"critical"}, Invert: true},
			critical, false, true},
		{"inverted miss is admitted (the everything-else hook)",
			WebhookFilter{CategoryIDs: []uuid.UUID{catA}, Priorities: []string{"critical"}, Invert: true},
			withPriority(critical, "low"), true, true},
		{"a value that no longer exists matches nothing",
			WebhookFilter{CategoryIDs: []uuid.UUID{uuid.New()}}, critical, false, true},
		{"fail open when attrs are unknown",
			WebhookFilter{Priorities: []string{"critical"}}, nil, true, false},
		{"fail open ignores invert",
			WebhookFilter{Priorities: []string{"critical"}, Invert: true}, nil, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			admit, known := tc.filter.Admits(tc.attrs)
			if admit != tc.wantAdmit || known != tc.wantKnown {
				t.Fatalf("Admits() = (admit %v, known %v), want (admit %v, known %v)",
					admit, known, tc.wantAdmit, tc.wantKnown)
			}
		})
	}
}

// The API always answers with all four keys, and with arrays, never null: the
// admin page maps over the lists.
func TestWebhookFilter_EncodesEmptyListsAsArrays(t *testing.T) {
	got, err := json.Marshal(WebhookFilter{})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"category_ids":[],"priorities":[],"group_ids":[],"invert":false}`
	if string(got) != want {
		t.Fatalf("json.Marshal(WebhookFilter{}) = %s, want %s", got, want)
	}
}

func TestWebhookFilter_RoundTrips(t *testing.T) {
	want := WebhookFilter{
		CategoryIDs: []uuid.UUID{uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")},
		Priorities:  []string{"critical", "high"},
		GroupIDs:    []uuid.UUID{uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000001")},
		Invert:      true,
	}
	raw, err := json.Marshal(want)
	require.NoError(t, err)
	var got WebhookFilter
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, want, got)
}

// {} is what the store holds for "no filter", so it must read back as one.
func TestWebhookFilter_DecodesAnEmptyObjectAsNoFilter(t *testing.T) {
	var f WebhookFilter
	require.NoError(t, json.Unmarshal([]byte(`{}`), &f))
	require.True(t, f.IsEmpty())
	require.False(t, f.Invert)
}

// Type and item are conditions like category: any-of within the list, AND across
// conditions (#631, owner decision D36). The first case is the one that matters
// most: a filter with only a type must not read as "no filter", or the hook
// would fire for every ticket.
func TestWebhookFilter_TypeAndItem(t *testing.T) {
	cat := uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
	typA := uuid.MustParse("cccccccc-0000-0000-0000-000000000001")
	typB := uuid.MustParse("cccccccc-0000-0000-0000-000000000002")
	itemA := uuid.MustParse("dddddddd-0000-0000-0000-000000000001")
	itemB := uuid.MustParse("dddddddd-0000-0000-0000-000000000002")
	full := &TicketState{CategoryID: cat, Priority: "low", TypeID: &typA, ItemID: &itemA}
	noType := &TicketState{CategoryID: cat, Priority: "low"}

	if (WebhookFilter{TypeIDs: []uuid.UUID{typA}}).IsEmpty() {
		t.Error("a filter with only a type is not empty")
	}
	if (WebhookFilter{ItemIDs: []uuid.UUID{itemA}}).IsEmpty() {
		t.Error("a filter with only an item is not empty")
	}

	cases := []struct {
		name      string
		filter    WebhookFilter
		attrs     *TicketState
		wantAdmit bool
	}{
		{"type match", WebhookFilter{TypeIDs: []uuid.UUID{typA}}, full, true},
		{"type any-of", WebhookFilter{TypeIDs: []uuid.UUID{typB, typA}}, full, true},
		{"type miss", WebhookFilter{TypeIDs: []uuid.UUID{typB}}, full, false},
		{"a ticket with no type never matches a type", WebhookFilter{TypeIDs: []uuid.UUID{typA}}, noType, false},
		{"item match", WebhookFilter{ItemIDs: []uuid.UUID{itemA}}, full, true},
		{"item miss", WebhookFilter{ItemIDs: []uuid.UUID{itemB}}, full, false},
		{"a ticket with no item never matches an item", WebhookFilter{ItemIDs: []uuid.UUID{itemA}}, noType, false},
		{"type and category together (AND), both hold",
			WebhookFilter{CategoryIDs: []uuid.UUID{cat}, TypeIDs: []uuid.UUID{typA}}, full, true},
		{"type and category together (AND), category misses",
			WebhookFilter{CategoryIDs: []uuid.UUID{uuid.New()}, TypeIDs: []uuid.UUID{typA}}, full, false},
		{"type and category together (AND), type misses",
			WebhookFilter{CategoryIDs: []uuid.UUID{cat}, TypeIDs: []uuid.UUID{typB}}, full, false},
		{"type and item together, item misses",
			WebhookFilter{TypeIDs: []uuid.UUID{typA}, ItemIDs: []uuid.UUID{itemB}}, full, false},
		{"inverted type: the matching ticket is refused", WebhookFilter{TypeIDs: []uuid.UUID{typA}, Invert: true}, full, false},
		{"inverted type: another type is admitted", WebhookFilter{TypeIDs: []uuid.UUID{typB}, Invert: true}, full, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			admit, known := tc.filter.Admits(tc.attrs)
			if admit != tc.wantAdmit || !known {
				t.Fatalf("Admits() = (admit %v, known %v), want (admit %v, known true)", admit, known, tc.wantAdmit)
			}
		})
	}
	if admit, known := (WebhookFilter{TypeIDs: []uuid.UUID{typA}}).Admits(nil); !admit || known {
		t.Fatalf("unknown attributes fail open for a type filter too: got (admit %v, known %v)", admit, known)
	}
}

// The two new lists are left out of the JSON while empty, so a filter that does
// not use them reads exactly as it did before they existed. They are written
// as arrays once they hold something, and round-trip.
func TestWebhookFilter_TypeAndItemEncoding(t *testing.T) {
	typ := uuid.MustParse("cccccccc-0000-0000-0000-000000000001")
	item := uuid.MustParse("dddddddd-0000-0000-0000-000000000001")
	want := WebhookFilter{TypeIDs: []uuid.UUID{typ}, ItemIDs: []uuid.UUID{item}}
	raw, err := json.Marshal(want)
	require.NoError(t, err)
	require.JSONEq(t, `{"category_ids":[],"priorities":[],"group_ids":[],"type_ids":["`+typ.String()+`"],"item_ids":["`+item.String()+`"],"invert":false}`, string(raw))
	var got WebhookFilter
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, want.TypeIDs, got.TypeIDs)
	require.Equal(t, want.ItemIDs, got.ItemIDs)
}
