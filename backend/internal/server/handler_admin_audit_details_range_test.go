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
	maxSpan := time.Duration(audit.DetailsSearchMaxDays) * day

	r, status := search("zz-details-needle", 30*day)
	require.Equal(t, http.StatusOK, status)
	require.True(t, hit(r), "inside a range, the details are searched")
	require.True(t, r.DetailsSearched)
	require.Empty(t, r.DetailsSkippedReason)

	r, status = search("zz-details-needle", maxSpan)
	require.Equal(t, http.StatusOK, status, "exactly the limit is allowed")
	require.True(t, hit(r))
	require.True(t, r.DetailsSearched)

	r, status = search("zz-details-needle", maxSpan+time.Second)
	require.Equal(t, http.StatusBadRequest, status, "a second over the limit is refused")
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
