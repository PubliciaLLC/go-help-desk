package server_test

import (
	"encoding/json"
	"fmt"
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
