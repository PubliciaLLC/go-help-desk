package server_test

import (
	"net/http"
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/stretchr/testify/require"
)

// An omitted priority is the catch-all tier DESIGN.md documents, and an unknown
// one used to reach the column's CHECK constraint — a 500 for what is plainly a
// bad request.

func TestCreateSLAPolicy_PriorityIsOptionalAndValidated(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	cases := []struct {
		name         string
		body         map[string]any
		wantStatus   int
		wantPriority *ticket.Priority
	}{
		{
			name:       "omitted priority creates a catch-all",
			body:       map[string]any{"name": "Catch-all"},
			wantStatus: http.StatusCreated,
		},
		{
			name:         "a known priority is kept",
			body:         map[string]any{"name": "High", "priority": "high"},
			wantStatus:   http.StatusCreated,
			wantPriority: priorityOf(ticket.PriorityHigh),
		},
		{
			name:       "an unknown priority is rejected",
			body:       map[string]any{"name": "Bogus", "priority": "urgent"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "an empty priority is rejected rather than stored",
			body:       map[string]any{"name": "Empty", "priority": ""},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.body["response_target_min"] = 60
			tc.body["resolution_target_min"] = 480

			resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/sla/policies", tc.body)
			require.Equal(t, tc.wantStatus, resp.StatusCode)
			if tc.wantStatus != http.StatusCreated {
				// The status alone does not distinguish a rejection from a
				// database CHECK violation reported as one: the handler maps
				// every store error to 400 too. The message is what says the
				// request never reached the column.
				var errBody struct {
					Error struct {
						Message string `json:"message"`
					} `json:"error"`
				}
				decodeJSON(t, resp, &errBody)
				require.Contains(t, errBody.Error.Message, "invalid priority")
				return
			}
			var got sla.Policy
			decodeJSON(t, resp, &got)
			require.Equal(t, tc.wantPriority, got.Priority)
		})
	}
}

// clear_priority exists for the same reason clear_category does: a JSON null
// and an absent key both decode to a nil pointer, so without it the UI could
// never turn a priority-specific policy back into a catch-all.
func TestUpdateSLAPolicy_ClearPriority(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/sla/policies", map[string]any{
		"name": "High", "priority": "high",
		"response_target_min": 60, "resolution_target_min": 480,
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created sla.Policy
	decodeJSON(t, resp, &created)
	require.NotNil(t, created.Priority)

	path := "/api/v1/admin/sla/policies/" + created.ID.String()

	// A patch that does not mention priority leaves it alone.
	resp = h.doAsAdmin(t, http.MethodPatch, path, map[string]any{"name": "Renamed"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var renamed sla.Policy
	decodeJSON(t, resp, &renamed)
	require.Equal(t, created.Priority, renamed.Priority)

	// clear_priority widens it to every priority.
	resp = h.doAsAdmin(t, http.MethodPatch, path, map[string]any{"clear_priority": true})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var cleared sla.Policy
	decodeJSON(t, resp, &cleared)
	require.Nil(t, cleared.Priority)

	// The change survives a round trip through the database.
	resp = h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/sla/policies", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var list []sla.Policy
	decodeJSON(t, resp, &list)
	var found bool
	for _, p := range list {
		if p.ID == created.ID {
			found = true
			require.Nil(t, p.Priority)
		}
	}
	require.True(t, found)

	// An unknown priority on update is rejected before the column's CHECK sees
	// it — an aborted transaction would take the rest of this request with it.
	resp = h.doAsAdmin(t, http.MethodPatch, path, map[string]any{"priority": "urgent"})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	var errBody struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeJSON(t, resp, &errBody)
	require.Contains(t, errBody.Error.Message, "invalid priority")
}

func priorityOf(p ticket.Priority) *ticket.Priority { return &p }
