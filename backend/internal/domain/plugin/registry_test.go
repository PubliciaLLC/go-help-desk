package plugin_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/plugin"
)

// Enable and Disable on an id that was never registered return ErrNotFound,
// which the HTTP layer maps to 404 (#418).
func TestRegistry_UnknownPluginIsErrNotFound(t *testing.T) {
	for name, call := range map[string]func(plugin.Registry) error{
		"enable":  func(r plugin.Registry) error { return r.Enable("nope") },
		"disable": func(r plugin.Registry) error { return r.Disable("nope") },
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, call(plugin.NewRegistry()), plugin.ErrNotFound)
		})
	}
}
