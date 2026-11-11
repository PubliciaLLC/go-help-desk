package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
)

// windowStore answers every search with a fixed page, for what the real store
// only produces at DiffSearchWindow entries.
type windowStore struct {
	audit.Store
	reached bool
}

func (w windowStore) Search(context.Context, audit.Filter, int, int) (audit.Page, error) {
	return audit.Page{DetailsWindowReached: w.reached}, nil
}

// The page says when the search of the change details stopped at the window, so
// the log can tell the viewer to narrow it. Always sent, false included, like
// total_capped.
func TestAdminAudit_SaysWhenTheChangeDetailsWindowWasReached(t *testing.T) {
	for _, reached := range []bool{true, false} {
		h, cleanup := newHarness(t)
		h.srv.SetAuditStoreForTest(windowStore{Store: h.auditStore, reached: reached})

		res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/audit?q=anything", nil)
		require.Equal(t, http.StatusOK, res.StatusCode)
		var page map[string]any
		decodeJSON(t, res, &page)
		res.Body.Close()
		require.Equal(t, reached, page["details_window_reached"], "reached=%v", reached)
		cleanup()
	}
}
