package server_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
)

// The change details are searched only inside a date range of at most
// audit.DetailsSearchMaxDays (#629, owner 2026-11-11). With text and no range
// they are left out and the response says so. With text and a range over the
// limit the request is refused with a 400 (Erik, 2026-11-11): a range that long
// cannot be searched, and silently searching less than was asked for is worse
// than saying so.
func TestAdminAudit_ChangeDetailsAreSearchedOnlyInsideADateRange(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	entityID := uuid.New()
	require.NoError(t, h.auditStore.Create(context.Background(), audit.Entry{
		ID: uuid.New(), ActorID: &h.adminID, EntityType: "setting", EntityID: entityID, Action: "details_range_probe",
		After: map[string]any{"scope_path": "zz-details-needle"},
	}))
	// The store stamps the entry itself; every range ends a minute after it.
	end := time.Now().UTC().Truncate(time.Second).Add(time.Minute)

	type result struct {
		Entries []struct {
			EntityID string `json:"entity_id"`
		} `json:"entries"`
		DetailsSearched      bool   `json:"details_searched"`
		DetailsSkippedReason string `json:"details_skipped_reason"`
		Error                struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	day := 24 * time.Hour
	// A span of 0 sends no range at all; q "" sends no text.
	search := func(q string, span time.Duration) (res result, status int) {
		v := url.Values{"limit": {"200"}}
		if q != "" {
			v.Set("q", q)
		}
		if span > 0 {
			v.Set("from", end.Add(-span).Format(time.RFC3339))
			v.Set("to", end.Format(time.RFC3339))
		}
		r := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/audit?"+v.Encode(), nil)
		decodeJSON(t, r, &res)
		r.Body.Close()
		return res, r.StatusCode
	}
	hit := func(r result) bool {
		for _, e := range r.Entries {
			if e.EntityID == entityID.String() {
				return true
			}
		}
		return false
	}
	exactly90 := time.Duration(audit.DetailsSearchMaxDays) * day
	maxSpan := audit.DetailsSearchMaxSpan // 90 days and an hour, see its comment

	r, status := search("zz-details-needle", 30*day)
	require.Equal(t, http.StatusOK, status)
	require.True(t, hit(r), "inside a range, the details are searched")
	require.True(t, r.DetailsSearched)
	require.Empty(t, r.DetailsSkippedReason)

	r, status = search("zz-details-needle", exactly90)
	require.Equal(t, http.StatusOK, status, "exactly 90 days is allowed")
	require.True(t, hit(r))
	require.True(t, r.DetailsSearched)

	r, status = search("zz-details-needle", maxSpan)
	require.Equal(t, http.StatusOK, status, "90 days and an hour is allowed: it is what a clock change makes of 90 calendar days")
	require.True(t, r.DetailsSearched)

	r, status = search("zz-details-needle", maxSpan+time.Second)
	require.Equal(t, http.StatusBadRequest, status, "a second over that is refused")
	require.Equal(t, "date_range_too_long", r.Error.Code)
	require.Contains(t, r.Error.Message, "90 days", "and the message names the limit")
	require.Empty(t, r.Entries)

	r, status = search("details_range_probe", 400*day)
	require.Equal(t, http.StatusBadRequest, status, "the refusal does not depend on what the text would match")
	require.Equal(t, "date_range_too_long", r.Error.Code)

	r, status = search("zz-details-needle", 0)
	require.Equal(t, http.StatusOK, status)
	require.False(t, hit(r), "no range, no search of the details")
	require.False(t, r.DetailsSearched)
	require.Equal(t, audit.DetailsSkippedNoRange, r.DetailsSkippedReason)

	// What is cheap to search is still searched, with no range at all.
	r, _ = search("details_range_probe", 0)
	require.True(t, hit(r), "by action")
	require.Equal(t, audit.DetailsSkippedNoRange, r.DetailsSkippedReason, "and the response still says the details were left out")

	// Without text there is nothing to search and no reason to limit the range.
	r, status = search("", 400*day)
	require.Equal(t, http.StatusOK, status, "a long range with no text is a plain date filter")
	require.True(t, hit(r))
	require.False(t, r.DetailsSearched)
	require.Empty(t, r.DetailsSkippedReason)

	r, status = search("", 0)
	require.Equal(t, http.StatusOK, status)
	require.False(t, r.DetailsSearched)
	require.Empty(t, r.DetailsSkippedReason)
}

// The page picks local date-times, so "90 calendar days" is 90 days and an hour
// when the range crosses the clock change that gives a day 25 hours, and 90 days
// less an hour when it crosses the one that gives a day 23. Both must work with
// text, or the page would offer a range its own server refuses.
func TestAdminAudit_NinetyCalendarDaysAcrossAClockChangeIsAllowed(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	for _, tc := range []struct{ name, from, to string }{
		// US Central: clocks go back on 2026-11-01 (a 25-hour day), forward on 2026-03-08 (23).
		{"fall back, 90 days and an hour", "2026-09-01T00:00:00-05:00", "2026-11-30T00:00:00-06:00"},
		{"spring forward, 90 days less an hour", "2026-02-01T00:00:00-06:00", "2026-05-02T00:00:00-05:00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := url.Values{"q": {"anything"}, "from": {tc.from}, "to": {tc.to}}
			r := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/audit?"+v.Encode(), nil)
			var res struct {
				DetailsSearched bool `json:"details_searched"`
			}
			decodeJSON(t, r, &res)
			r.Body.Close()
			require.Equal(t, http.StatusOK, r.StatusCode)
			require.True(t, res.DetailsSearched)
		})
	}
}

// A range that ends before it starts is nonsense to search in, so with text it
// is refused. Without text the list keeps answering as it always has, with
// nothing, because refusing it would change a response that has worked since
// the filter existed.
func TestAdminAudit_ReversedRangeIsRefusedWithTextAndEmptyWithout(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	later, earlier := "2026-06-02T00:00:00Z", "2026-06-01T00:00:00Z"

	v := url.Values{"q": {"anything"}, "from": {later}, "to": {earlier}}
	r := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/audit?"+v.Encode(), nil)
	var refused struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	decodeJSON(t, r, &refused)
	r.Body.Close()
	require.Equal(t, http.StatusBadRequest, r.StatusCode)
	require.Equal(t, "invalid_date_range", refused.Error.Code)
	require.Contains(t, refused.Error.Message, "from")

	v.Del("q")
	r = h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/audit?"+v.Encode(), nil)
	var page struct {
		Entries []any `json:"entries"`
	}
	decodeJSON(t, r, &page)
	r.Body.Close()
	require.Equal(t, http.StatusOK, r.StatusCode, "no text: unchanged")
	require.Empty(t, page.Entries)
}
