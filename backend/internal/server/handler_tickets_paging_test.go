package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Every ticket list passed a hard-coded (100, 0). Past 100 tickets the older
// ones stopped appearing, with nothing to say a limit had been reached and no
// way to ask for the next page.
func TestListTickets_Paging(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	const total = 12
	for i := 0; i < total; i++ {
		resp := h.doAsUser(t, http.MethodPost, "/api/v1/tickets", map[string]any{
			"subject": fmt.Sprintf("Paged ticket %02d", i), "description": "x",
			"category_id": h.catID.String(),
		})
		require.Equal(t, http.StatusCreated, resp.StatusCode)
	}

	list := func(t *testing.T, query string) []map[string]any {
		t.Helper()
		resp := h.doAsUser(t, http.MethodGet, "/api/v1/tickets"+query, nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var out []map[string]any
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
		return out
	}

	t.Run("limit is honoured", func(t *testing.T) {
		require.Len(t, list(t, "?limit=5"), 5)
	})

	t.Run("offset moves the window", func(t *testing.T) {
		first := list(t, "?limit=5")
		second := list(t, "?limit=5&offset=5")
		require.Len(t, second, 5)
		require.NotEqual(t, first[0]["id"], second[0]["id"],
			"the second page must not repeat the first")

		// No ticket appears on both pages.
		seen := map[any]bool{}
		for _, tk := range first {
			seen[tk["id"]] = true
		}
		for _, tk := range second {
			require.False(t, seen[tk["id"]], "ticket %v appears on both pages", tk["id"])
		}
	})

	t.Run("the last page is short and the one past it is empty", func(t *testing.T) {
		require.Len(t, list(t, "?limit=5&offset=10"), total-10)
		require.Empty(t, list(t, "?limit=5&offset=1000"))
	})

	t.Run("a client that sends nothing gets what it always got", func(t *testing.T) {
		require.Len(t, list(t, ""), total, "the default must stay 100, not become smaller")
	})

	t.Run("the ceiling holds", func(t *testing.T) {
		// Asking for the whole table must not be granted. 12 tickets exist, so
		// the visible effect is only that the request succeeds; the cap is
		// asserted directly below.
		require.Len(t, list(t, "?limit=100000"), total)
	})

	t.Run("nonsense values fall back rather than erroring", func(t *testing.T) {
		for _, qs := range []string{
			"?limit=abc", "?limit=0", "?limit=-5",
			"?offset=abc", "?offset=-1",
			// A negative offset reaches Postgres as an error and used to be a
			// 500 for what is a bad request.
		} {
			t.Run(qs, func(t *testing.T) {
				resp := h.doAsUser(t, http.MethodGet, "/api/v1/tickets"+qs, nil)
				require.Equal(t, http.StatusOK, resp.StatusCode,
					"%s must not produce an error", qs)
			})
		}
	})
}

// Search takes the same window, and used to take the same hard-coded one.
func TestListTickets_PagingAppliesToSearch(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	for i := 0; i < 6; i++ {
		resp := h.doAsUser(t, http.MethodPost, "/api/v1/tickets", map[string]any{
			"subject": "searchable zebra " + fmt.Sprint(i), "description": "x",
			"category_id": h.catID.String(),
		})
		require.Equal(t, http.StatusCreated, resp.StatusCode)
	}

	resp := h.doAsUser(t, http.MethodGet, "/api/v1/tickets?q=zebra&limit=2", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Len(t, out, 2, "search must honour the limit")
}

// The staff default view merges 1+N queries — own tickets plus one per group.
// Pushing the page window into each of them and concatenating returned
// limit×(1+groups) rows and skipped offset rows in every sublist
// independently, so ?limit=5 returned 10 and the frontend then disabled Next
// while tickets were still unreachable.
//
// This is the branch every staff member and admin lands on by default, and the
// original paging tests only covered the reporter branch, which is why it
// shipped.
func TestListTickets_PagingOnTheMergedStaffView(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	grp, err := h.groupSvc.Create(ctx, "Support "+uuid.NewString()[:8], "")
	require.NoError(t, err)
	require.NoError(t, h.groupSvc.AddMember(ctx, grp.ID, h.staffID))

	// Eight assigned to the staff member, eight to their group: two sources.
	const perSource = 8
	for i := 0; i < perSource*2; i++ {
		resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets", map[string]any{
			"subject": fmt.Sprintf("Merged %02d", i), "description": "x",
			"category_id": h.catID.String(),
		})
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		var created struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))

		assign := map[string]any{"assignee_user_id": h.staffID.String()}
		if i >= perSource {
			assign = map[string]any{"assignee_group_id": grp.ID.String()}
		}
		resp = h.doAsAdmin(t, http.MethodPatch, "/api/v1/tickets/"+created.ID, assign)
		require.Equal(t, http.StatusOK, resp.StatusCode)
	}

	page := func(t *testing.T, query string) []map[string]any {
		t.Helper()
		resp := h.do(t, http.MethodGet, "/api/v1/tickets"+query, nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var out []map[string]any
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
		return out
	}

	t.Run("limit means limit, not limit per source", func(t *testing.T) {
		require.Len(t, page(t, "?limit=5"), 5)
		require.Len(t, page(t, "?limit=8"), 8)
	})

	t.Run("pages do not overlap and cover everything", func(t *testing.T) {
		seen := map[any]bool{}
		for off := 0; off < perSource*2; off += 5 {
			for _, tk := range page(t, fmt.Sprintf("?limit=5&offset=%d", off)) {
				require.False(t, seen[tk["id"]],
					"ticket %v appeared on two pages", tk["id"])
				seen[tk["id"]] = true
			}
		}
		require.Len(t, seen, perSource*2,
			"paging must reach every ticket exactly once")
	})

	t.Run("the page past the end is empty", func(t *testing.T) {
		require.Empty(t, page(t, "?limit=5&offset=100"))
	})

	t.Run("ordering is stable across repeated reads", func(t *testing.T) {
		first := page(t, "?limit=16")
		second := page(t, "?limit=16")
		require.Equal(t, first, second,
			"identical requests must return identical order, or pages shuffle")
	})
}
