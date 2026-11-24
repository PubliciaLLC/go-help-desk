package report_test

import (
	"reflect"
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/report"
)

func intp(n int) *int { return &n }

func TestBacklogBuckets(t *testing.T) {
	want := []report.BacklogBucket{
		{Key: "0_2", MinDays: 0, MaxDays: intp(2)},
		{Key: "3_7", MinDays: 3, MaxDays: intp(7)},
		{Key: "8_30", MinDays: 8, MaxDays: intp(30)},
		{Key: "over_30", MinDays: 31, MaxDays: nil},
	}
	if !reflect.DeepEqual(report.BacklogBuckets, want) {
		t.Errorf("BacklogBuckets = %+v, want %+v", report.BacklogBuckets, want)
	}
}

func TestBuckets(t *testing.T) {
	cases := []struct {
		name string
		rows []report.AgeCount
		want map[string]int64
	}{
		{
			name: "no rows is four zeros",
			rows: nil,
			want: map[string]int64{"0_2": 0, "3_7": 0, "8_30": 0, "over_30": 0},
		},
		{
			name: "each boundary age lands in its own bucket",
			rows: []report.AgeCount{
				{Days: 0, Count: 1}, {Days: 2, Count: 1}, {Days: 3, Count: 1}, {Days: 7, Count: 1},
				{Days: 8, Count: 1}, {Days: 30, Count: 1}, {Days: 31, Count: 1},
			},
			want: map[string]int64{"0_2": 2, "3_7": 2, "8_30": 2, "over_30": 1},
		},
		{
			name: "counts add within a bucket",
			rows: []report.AgeCount{{Days: 1, Count: 4}, {Days: 2, Count: 5}, {Days: 9, Count: 6}, {Days: 31, Count: 7}},
			want: map[string]int64{"0_2": 9, "3_7": 0, "8_30": 6, "over_30": 7},
		},
		{
			name: "a ticket created after now is age 0",
			rows: []report.AgeCount{{Days: -1, Count: 3}},
			want: map[string]int64{"0_2": 3, "3_7": 0, "8_30": 0, "over_30": 0},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := report.Buckets(tc.rows)
			if len(got) != 4 {
				t.Fatalf("Buckets returned %d buckets, want 4", len(got))
			}
			for i, b := range got {
				if b.Key != report.BacklogBuckets[i].Key {
					t.Errorf("bucket %d is %s, want %s", i, b.Key, report.BacklogBuckets[i].Key)
				}
				if b.Count != tc.want[b.Key] {
					t.Errorf("%s = %d, want %d", b.Key, b.Count, tc.want[b.Key])
				}
			}
		})
	}
}
