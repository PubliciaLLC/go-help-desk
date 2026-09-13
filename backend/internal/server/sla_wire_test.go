package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

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
