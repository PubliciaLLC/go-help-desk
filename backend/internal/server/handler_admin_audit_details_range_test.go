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
// audit.DetailsSearchMaxDays (#629, owner 2026-11-11). A longer range is treated
// as no range, not refused: the entity, action and actor are still searched, the
// date filter still applies, and the response says why the details were not, so
// a person editing the dates keeps getting a page and an explanation, not a 400.
func TestAdminAudit_ChangeDetailsAreSearchedOnlyInsideADateRange(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	now := time.Now().UTC()
	entityID := uuid.New()
	require.NoError(t, h.auditStore.Create(context.Background(), audit.Entry{
		ID: uuid.New(), ActorID: &h.adminID, EntityType: "setting", EntityID: entityID, Action: "details_range_probe",
		After: map[string]any{"scope_path": "zz-details-needle"}, CreatedAt: now,
	}))

	type result struct {
		Entries []struct {
			EntityID string `json:"entity_id"`
		} `json:"entries"`
		DetailsSearched      bool   `json:"details_searched"`
		DetailsSkippedReason string `json:"details_skipped_reason"`
	}
	search := func(q string, days int) (res result, status int) {
		v := url.Values{"q": {q}, "limit": {"200"}}
		if days > 0 {
			v.Set("from", now.AddDate(0, 0, -days).Format(time.RFC3339))
			v.Set("to", now.Add(time.Minute).Format(time.RFC3339))
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

	r, status := search("zz-details-needle", 30)
	require.Equal(t, http.StatusOK, status)
	require.True(t, hit(r), "inside a range, the details are searched")
	require.True(t, r.DetailsSearched)
	require.Empty(t, r.DetailsSkippedReason)

	r, status = search("zz-details-needle", 0)
	require.Equal(t, http.StatusOK, status)
	require.False(t, hit(r), "no range, no search of the details")
	require.False(t, r.DetailsSearched)
	require.Equal(t, audit.DetailsSkippedNoRange, r.DetailsSkippedReason)

	r, status = search("zz-details-needle", audit.DetailsSearchMaxDays+1)
	require.Equal(t, http.StatusOK, status, "a range that is too long is not an error")
	require.False(t, hit(r))
	require.False(t, r.DetailsSearched)
	require.Equal(t, audit.DetailsSkippedRangeTooLong, r.DetailsSkippedReason)

	// What is cheap to search is still searched, with no range at all.
	r, _ = search("details_range_probe", 0)
	require.True(t, hit(r), "by action")
	require.Equal(t, audit.DetailsSkippedNoRange, r.DetailsSkippedReason, "and the response still says the details were left out")

	// Without text there is nothing to look for, so nothing was skipped.
	rr := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/audit", nil)
	var none result
	decodeJSON(t, rr, &none)
	rr.Body.Close()
	require.False(t, none.DetailsSearched)
	require.Empty(t, none.DetailsSkippedReason)
}
