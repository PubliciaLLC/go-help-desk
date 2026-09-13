package plugin_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/plugin"
)

// handleListPlugins marshals these straight to the client, and they had no
// tags — so the endpoint served Go field names, the same defect that made the
// admin SLA table unreadable (#123). Nothing consumes plugins in the UI yet,
// which is the only reason it was latent rather than broken.
//
// Asserting the exact keys rather than round-tripping: a round trip through
// the same struct passes whatever the names are.
func TestPlugin_JSONContract(t *testing.T) {
	p := plugin.Plugin{
		Manifest: plugin.Manifest{
			ID: "com.example.slack", Name: "Slack", Version: "1.0.0",
			Description: "Posts to Slack", Author: "Example",
		},
		Enabled:     true,
		WASMPath:    "/var/lib/ghd/plugins/slack.wasm",
		InstalledAt: time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC),
	}

	b, err := json.Marshal(p)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(b, &got))

	require.Contains(t, got, "manifest")
	require.Contains(t, got, "enabled")
	require.Contains(t, got, "installed_at")
	require.NotContains(t, got, "Manifest", "PascalCase means the client reads undefined")

	// A server filesystem path is not the browser's business. It also tells a
	// reader where the install directory is, which is free reconnaissance.
	require.NotContains(t, got, "wasm_path")
	require.NotContains(t, got, "WASMPath")
	require.NotContains(t, string(b), "/var/lib/ghd")

	manifest := got["manifest"].(map[string]any)
	for _, k := range []string{"id", "name", "version", "description", "author", "hooks", "runtime"} {
		require.Contains(t, manifest, k)
	}
}
