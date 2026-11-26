package server

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// PATCH /admin/settings refuses a name the spec does not list, so a setting the
// server accepts but components.schemas.SettingsValues does not declare is a
// document that lies about its own API. #711's two settings are declared as
// booleans and say they are session only, as they are in AuthCriticalKeys.
//
// Only these two: other settings are also missing from SettingsValues (the
// scope, auto-assign and server-kept keys), which is a gap of its own.
func TestOpenAPISpec_SettingsValuesDeclareTheChoiceSettings(t *testing.T) {
	doc := loadSpec(t)
	comps, _ := doc.root["components"].(map[string]any)
	schemas, _ := comps["schemas"].(map[string]any)
	values, _ := schemas["SettingsValues"].(map[string]any)
	props, _ := values["properties"].(map[string]any)
	require.NotEmpty(t, props)

	for _, k := range []string{admin.KeyUsersShowItems, admin.KeyGuestsShowTypes, admin.KeyGuestsShowItems} {
		require.Contains(t, admin.AuthCriticalKeys(), k)
		p, ok := props[k].(map[string]any)
		require.True(t, ok, "SettingsValues does not declare %q", k)
		require.Equal(t, "boolean", p["type"], k)
		desc, _ := p["description"].(string)
		require.True(t, strings.Contains(desc, "Session only"), "%s: the description must say Session only", k)
	}
}
