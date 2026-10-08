package server_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// #331: `total` stops at audit.TotalCap and `total_capped` says it stopped.
// The pager must not care: has_more is read off the page, so it is right on
// both sides of the cap, for the admin and for staff alike — through the one
// store call both go through.

type cappedPage struct {
	Entries     []struct{ ID string } `json:"entries"`
	Total       *int                  `json:"total"`
	TotalCapped *bool                 `json:"total_capped"`
	HasMore     bool                  `json:"has_more"`
}

func TestAdminAudit_TotalCapBoundary(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	// A ticket staff may see (assigned to them), with scope enforced, so the
	// staff path is the scoped one. Admins are never scoped.
	enableScope(t, h)
	newSt, err := h.ticketStore.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Millisecond)
	tk := ticket.Ticket{
		ID: uuid.New(), TrackingNumber: ticket.GenerateTrackingNumber(ticket.DefaultTrackingPrefix, 2026, 9331),
		Subject: "cap", CategoryID: h.catID, Priority: ticket.PriorityLow, StatusID: newSt.ID,
		AssigneeUserID: &h.staffID, CreatedAt: now, UpdatedAt: now,
	}
	guest := "guest@cap.local"
	tk.GuestEmail = &guest
	require.NoError(t, h.ticketStore.Create(ctx, tk))

	const action = "cap_http"
	const c = audit.TotalCap
	newest := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	planted := 0
	growTo := func(n int) {
		t.Helper()
		_, err := h.tx.ExecContext(ctx, `
			INSERT INTO audit_log (id, entity_type, entity_id, action, created_at)
			SELECT gen_random_uuid(), 'ticket', $1, $2, $3::timestamptz - g * interval '1 second'
			FROM generate_series($4::int, $5::int - 1) g`,
			tk.ID, action, newest, planted, n)
		require.NoError(t, err)
		planted = n
	}

	read := func(t *testing.T, do func(*testing.T, string, string, any) *http.Response, limit, offset int) cappedPage {
		t.Helper()
		resp := do(t, http.MethodGet, fmt.Sprintf("/api/v1/admin/audit?action=%s&limit=%d&offset=%d", action, limit, offset), nil)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var p cappedPage
		decodeJSON(t, resp, &p)
		require.NotNil(t, p.Total)
		require.NotNil(t, p.TotalCapped, "total_capped must always be present, so a client never has to guess")
		return p
	}

	actors := []struct {
		name string
		do   func(*testing.T, string, string, any) *http.Response
	}{{"admin", h.doAsAdmin}, {"scoped staff", h.do}}

	for _, tc := range []struct {
		n          int
		wantTotal  int
		wantCapped bool
	}{
		{c - 1, c - 1, false},
		{c, c, false},
		{c + 1, c, true},
	} {
		growTo(tc.n)
		for _, a := range actors {
			t.Run(fmt.Sprintf("%s/%d entries", a.name, tc.n), func(t *testing.T) {
				first := read(t, a.do, 50, 0)
				require.Equal(t, tc.wantTotal, *first.Total)
				require.Equal(t, tc.wantCapped, *first.TotalCapped)
				require.Len(t, first.Entries, 50)
				require.True(t, first.HasMore)

				// The last entry in the table, whatever the total says.
				last := read(t, a.do, 1, tc.n-1)
				require.Len(t, last.Entries, 1)
				require.False(t, last.HasMore, "the final entry has nothing after it")
				require.Equal(t, tc.wantTotal, *last.Total)
				require.Equal(t, tc.wantCapped, *last.TotalCapped)

				// The one before it still has a next page.
				prev := read(t, a.do, 1, tc.n-2)
				require.Len(t, prev.Entries, 1)
				require.True(t, prev.HasMore)

				// The cap-th entry: the end of the table only when that is where it ends.
				if tc.n >= c {
					atCap := read(t, a.do, 1, c-1)
					require.Len(t, atCap.Entries, 1)
					require.Equal(t, tc.n > c, atCap.HasMore, "has_more at the cap-th entry must follow the table, not the capped total")
				}
			})
		}
	}
}

// A total under the cap is the same exact number it always was, and says so.
// The entries are planted under their own action, so the expected total is a
// number this test chose rather than whatever else the database holds.
func TestAdminAudit_TotalIsNotCappedForSmallInstances(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	const planted = 7
	for i := 0; i < planted; i++ {
		require.NoError(t, h.q.CreateAuditEntry(context.Background(), dbgen.CreateAuditEntryParams{
			ID: uuid.New(), EntityType: "ticket", EntityID: uuid.New(), Action: "fixture_small",
			CreatedAt: time.Now().Add(-time.Duration(i) * time.Second),
		}))
	}

	for _, do := range []func(*testing.T, string, string, any) *http.Response{h.doAsAdmin, h.do} {
		resp := do(t, http.MethodGet, "/api/v1/admin/audit?action=fixture_small&limit=1", nil)
		var p cappedPage
		decodeJSON(t, resp, &p)
		resp.Body.Close()
		require.NotNil(t, p.Total)
		require.NotNil(t, p.TotalCapped)
		require.Equal(t, planted, *p.Total)
		require.False(t, *p.TotalCapped)
		require.True(t, p.HasMore)
	}
}
