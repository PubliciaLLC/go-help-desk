package audit_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
)

func TestFilter_DetailsSearch(t *testing.T) {
	now := time.Now()
	day := 24 * time.Hour
	at := func(d time.Duration) *time.Time { t := now.Add(d); return &t }
	max := time.Duration(audit.DetailsSearchMaxDays) * day

	for _, tc := range []struct {
		name        string
		f           audit.Filter
		wantSearch  bool
		wantReason  string
		wantTooLong bool
	}{
		{"no text needs no range", audit.Filter{}, false, "", false},
		{"text, no range", audit.Filter{Q: "x"}, false, audit.DetailsSkippedNoRange, false},
		{"text, only from", audit.Filter{Q: "x", From: at(-day)}, false, audit.DetailsSkippedNoRange, false},
		{"text, only to", audit.Filter{Q: "x", To: at(0)}, false, audit.DetailsSkippedNoRange, false},
		{"text, a day", audit.Filter{Q: "x", From: at(-day), To: at(0)}, true, "", false},
		{"text, exactly the limit", audit.Filter{Q: "x", From: at(-max), To: at(0)}, true, "", false},
		{"text, over the limit", audit.Filter{Q: "x", From: at(-max - time.Second), To: at(0)}, false, "", true},
		{"no text, over the limit is just a date filter", audit.Filter{From: at(-max - time.Second), To: at(0)}, false, "", false},
		{"text, a range that ends before it starts holds nothing", audit.Filter{Q: "x", From: at(0), To: at(-day)}, true, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.wantSearch, tc.f.SearchesDetails())
			require.Equal(t, tc.wantReason, tc.f.DetailsSkippedReason())
			require.Equal(t, tc.wantTooLong, tc.f.DateRangeTooLong())
		})
	}
}
