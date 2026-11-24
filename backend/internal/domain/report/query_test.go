package report_test

import (
	"errors"
	"net/url"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/report"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

var (
	catID   = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	groupID = uuid.MustParse("22222222-2222-2222-2222-222222222222")
)

func mustDay(t *testing.T, s string) report.Day {
	t.Helper()
	d, err := report.ParseDay(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func ptrDay(t *testing.T, s string) *report.Day {
	t.Helper()
	d := mustDay(t, s)
	return &d
}

// wantFilterError checks err is a *ticket.FilterError of kind FilterInvalid
// with exactly msg, the wording the API answers with.
func wantFilterError(t *testing.T, err error, msg string) {
	t.Helper()
	var fe *ticket.FilterError
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v (%T), want *ticket.FilterError", err, err)
	}
	if fe.Kind != ticket.FilterInvalid {
		t.Errorf("Kind = %v, want FilterInvalid", fe.Kind)
	}
	if fe.Msg != msg {
		t.Errorf("Msg = %q, want %q", fe.Msg, msg)
	}
}

func TestParseQuery(t *testing.T) {
	cases := []struct {
		name    string
		q       url.Values
		want    report.Query
		wantErr string
	}{
		{
			name: "nothing set",
			q:    url.Values{},
			want: report.Query{},
		},
		{
			name: "tz is carried raw; the handler validates it",
			q:    url.Values{"tz": {"Europe/London"}},
			want: report.Query{TZ: "Europe/London"},
		},
		{
			name: "empty values are absent",
			q:    url.Values{"from": {""}, "to": {""}, "category_id": {""}, "priority": {""}, "assignee_group_id": {""}},
			want: report.Query{},
		},
		{
			name: "a whole range",
			q:    url.Values{"from": {"2026-10-01"}, "to": {"2026-10-31"}},
			want: report.Query{From: ptrDay(t, "2026-10-01"), To: ptrDay(t, "2026-10-31")},
		},
		{
			name:    "from without to",
			q:       url.Values{"from": {"2026-10-01"}},
			wantErr: "from and to go together",
		},
		{
			name:    "to without from",
			q:       url.Values{"to": {"2026-10-31"}},
			wantErr: "from and to go together",
		},
		{
			name:    "the from and to pairing comes before a bad from",
			q:       url.Values{"from": {"nope"}},
			wantErr: "from and to go together",
		},
		{
			name:    "bad from",
			q:       url.Values{"from": {"2026-02-29"}, "to": {"2026-03-01"}},
			wantErr: "invalid from",
		},
		{
			name:    "bad to",
			q:       url.Values{"from": {"2026-03-01"}, "to": {"2026-06-15T00:00:00Z"}},
			wantErr: "invalid to",
		},
		{
			name:    "bad from beats bad category",
			q:       url.Values{"from": {"x"}, "to": {"2026-03-01"}, "category_id": {"x"}},
			wantErr: "invalid from",
		},
		{
			name:    "bad to beats bad category",
			q:       url.Values{"from": {"2026-03-01"}, "to": {"x"}, "category_id": {"x"}},
			wantErr: "invalid to",
		},
		{
			name:    "bad category",
			q:       url.Values{"category_id": {"not-a-uuid"}, "assignee_group_id": {"x"}},
			wantErr: "invalid category_id",
		},
		{
			name:    "bad category beats bad priority",
			q:       url.Values{"category_id": {"x"}, "priority": {"urgent"}},
			wantErr: "invalid category_id",
		},
		{
			name: "a category",
			q:    url.Values{"category_id": {catID.String()}},
			want: report.Query{CategoryID: &catID},
		},
		{
			name:    "bad priority",
			q:       url.Values{"priority": {"urgent"}, "assignee_group_id": {"x"}},
			wantErr: "invalid priority",
		},
		{
			name:    "bad priority among good ones",
			q:       url.Values{"priority": {"high", "urgent"}},
			wantErr: "invalid priority",
		},
		{
			name:    "five priorities",
			q:       url.Values{"priority": {"low", "medium", "high", "critical", "low"}},
			wantErr: "too many priority values",
		},
		{
			name:    "too many beats an invalid one",
			q:       url.Values{"priority": {"low", "medium", "high", "critical", "urgent"}},
			wantErr: "too many priority values",
		},
		{
			name: "four priorities, sorted in PriorityOrder",
			q:    url.Values{"priority": {"low", "critical", "medium", "high"}},
			want: report.Query{Priorities: []ticket.Priority{
				ticket.PriorityCritical, ticket.PriorityHigh, ticket.PriorityMedium, ticket.PriorityLow,
			}},
		},
		{
			name: "one priority",
			q:    url.Values{"priority": {"medium"}},
			want: report.Query{Priorities: []ticket.Priority{ticket.PriorityMedium}},
		},
		{
			name:    "bad group, the last check",
			q:       url.Values{"assignee_group_id": {"x"}},
			wantErr: "invalid assignee_group_id",
		},
		{
			name: "a group",
			q:    url.Values{"assignee_group_id": {groupID.String()}},
			want: report.Query{GroupID: &groupID},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := report.ParseQuery(tc.q)
			if tc.wantErr != "" {
				wantFilterError(t, err, tc.wantErr)
				return
			}
			if err != nil {
				t.Fatalf("ParseQuery: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParseQuery = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestQueryPeriod(t *testing.T) {
	today := mustDay(t, "2026-11-24")
	cases := []struct {
		name    string
		q       report.Query
		want    report.Period
		wantErr string
	}{
		{
			name: "default is the 30 days ending today",
			q:    report.Query{},
			want: report.Period{From: mustDay(t, "2026-10-26"), To: today},
		},
		{
			name: "a single day",
			q:    report.Query{From: ptrDay(t, "2026-11-10"), To: ptrDay(t, "2026-11-10")},
			want: report.Period{From: mustDay(t, "2026-11-10"), To: mustDay(t, "2026-11-10")},
		},
		{
			name: "366 days is the most",
			q:    report.Query{From: ptrDay(t, "2025-11-24"), To: ptrDay(t, "2026-11-24")},
			want: report.Period{From: mustDay(t, "2025-11-24"), To: mustDay(t, "2026-11-24")},
		},
		{
			name:    "367 days is too long",
			q:       report.Query{From: ptrDay(t, "2025-11-23"), To: ptrDay(t, "2026-11-24")},
			wantErr: "the range is longer than 366 days",
		},
		{
			name:    "from after to",
			q:       report.Query{From: ptrDay(t, "2026-11-02"), To: ptrDay(t, "2026-11-01")},
			wantErr: "from must not be after to",
		},
		{
			name: "a to after today is allowed",
			q:    report.Query{From: ptrDay(t, "2026-11-20"), To: ptrDay(t, "2026-12-05")},
			want: report.Period{From: mustDay(t, "2026-11-20"), To: mustDay(t, "2026-12-05")},
		},
		{
			name:    "only one of from and to, built by hand",
			q:       report.Query{From: ptrDay(t, "2026-11-01")},
			wantErr: "from and to go together",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.q.Period(today)
			if tc.wantErr != "" {
				wantFilterError(t, err, tc.wantErr)
				return
			}
			if err != nil {
				t.Fatalf("Period: %v", err)
			}
			if got.From.String() != tc.want.From.String() || got.To.String() != tc.want.To.String() {
				t.Errorf("Period = %s..%s, want %s..%s", got.From, got.To, tc.want.From, tc.want.To)
			}
		})
	}
}
