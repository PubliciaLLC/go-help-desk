package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/stretchr/testify/require"
)

// End to end over HTTP, because the domain-level test pins the struct and this
// pins what an admin's browser actually receives. The admin SLA table was
// reading undefined for every field and nobody noticed, so "the struct is
// right" is not the assertion that matters here.
func TestSLAPolicyAPI_UsesSnakeCaseOnTheWire(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/sla/policies", map[string]any{
		"name":                  "Gold",
		"priority":              "high",
		"response_target_min":   30,
		"resolution_target_min": 240,
	})
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", body)

	var created map[string]any
	require.NoError(t, json.Unmarshal(body, &created))
	for _, k := range []string{"id", "name", "priority", "response_target_min", "resolution_target_min"} {
		require.Contains(t, created, k, "the create response must use the names the client reads")
	}
	require.NotContains(t, created, "ID", "PascalCase would mean the client reads undefined")

	res = h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/sla/policies", nil)
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	var list []map[string]any
	require.NoError(t, json.Unmarshal(body, &list))
	require.NotEmpty(t, list)
	require.Equal(t, "Gold", list[0]["name"])
	require.Equal(t, "high", list[0]["priority"])
}

// The per-ticket SLA status embedded on GET /tickets and GET /tickets/{id}
// (#183) is a second wire contract on top of Policy's: pinning the exact JSON
// here means a rename in status.go fails this test instead of silently
// changing what frontend/src/api/types.ts is supposed to describe.
func TestSLAStatus_JSONContract(t *testing.T) {
	metAt := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	status := sla.Status{
		PolicyID:   uuid.MustParse("66666666-6666-6666-6666-666666666666"),
		PolicyName: "Critical — 1h response",
		Response: sla.TargetStatus{
			Color: sla.Green, TargetMin: 60, ElapsedMin: 12, RemainingMin: 48, MetAt: &metAt,
		},
		Resolution: sla.TargetStatus{
			Color: sla.Amber, TargetMin: 480, ElapsedMin: 400, RemainingMin: 80, MetAt: nil,
		},
	}

	b, err := json.Marshal(status)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(b, &got))

	require.Equal(t, "66666666-6666-6666-6666-666666666666", got["policy_id"])
	require.Equal(t, "Critical — 1h response", got["policy_name"])

	response, ok := got["response"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "green", response["color"])
	require.EqualValues(t, 60, response["target_min"])
	require.EqualValues(t, 12, response["elapsed_min"])
	require.EqualValues(t, 48, response["remaining_min"])
	require.Contains(t, response, "met_at")
	require.NotNil(t, response["met_at"])

	resolution, ok := got["resolution"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "amber", resolution["color"])
	// met_at is present and explicitly null while outstanding, not omitted —
	// the same reasoning as ticketView.SLA itself: a client must be able to
	// tell "not met yet" from "this server predates the field".
	require.Contains(t, resolution, "met_at")
	require.Nil(t, resolution["met_at"])
}
