package server_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// Plugin and webhook PATCH on an unknown id answer 404, not 500 (#418).
func TestAdminPluginsAndWebhooks_ClientErrors(t *testing.T) {
	runClientErrorCases(t, []clientErrorCase{
		{"patch plugin unknown, enable", http.MethodPatch, func(t *testing.T, h *harness) (string, any) {
			return "/api/v1/admin/plugins/no-such-plugin", map[string]any{"enabled": true}
		}, 404, "not_found"},
		{"patch plugin unknown, disable", http.MethodPatch, func(t *testing.T, h *harness) (string, any) {
			return "/api/v1/admin/plugins/no-such-plugin", map[string]any{"enabled": false}
		}, 404, "not_found"},
		{"patch webhook unknown", http.MethodPatch, func(t *testing.T, h *harness) (string, any) {
			return "/api/v1/admin/webhooks/" + uuid.NewString(), map[string]any{"enabled": false}
		}, 404, "not_found"},
		{"patch webhook unknown, invalid body", http.MethodPatch, func(t *testing.T, h *harness) (string, any) {
			// The row is looked up before the body is read, so a missing hook
			// is a 404 whatever else is wrong with the request.
			return "/api/v1/admin/webhooks/" + uuid.NewString(), map[string]any{"url": "not a url"}
		}, 404, "not_found"},
	})
}
