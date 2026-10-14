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
	critical := &TicketAttrs{CategoryID: catA, Priority: "critical", AssigneeGroupID: &grpA}

	withPriority := func(a *TicketAttrs, p string) *TicketAttrs {
		c := *a
		c.Priority = p
		return &c
	}
	unassigned := func(a *TicketAttrs) *TicketAttrs {
		c := *a
		c.AssigneeGroupID = nil
		return &c
	}

	cases := []struct {
		name      string
		filter    WebhookFilter
		attrs     *TicketAttrs
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
