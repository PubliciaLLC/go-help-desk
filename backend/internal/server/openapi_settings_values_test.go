package server

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// PATCH /admin/settings refuses a name the spec does not list, so a setting the
// server accepts but components.schemas.SettingsValues does not declare is a
// document that lies about its own API. #711's three settings are declared as
// booleans and say they are session only, as they are in AuthCriticalKeys.
//
// Only these three: other settings are also missing from SettingsValues (the
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

// The texts that used to say a requester always receives item_id, never meets a
// 403 for filtering on one, or that a guest "picks a category and nothing
// below it" are guarded: they are wrong while the #711 settings exist.
func TestOpenAPISpec_ItemVisibilityIsStated(t *testing.T) {
	doc := loadSpec(t)
	comps, _ := doc.root["components"].(map[string]any)
	schemas, _ := comps["schemas"].(map[string]any)
	ticketSchema, _ := schemas["Ticket"].(map[string]any)
	desc, _ := ticketSchema["description"].(string)
	require.Contains(t, desc, "users_show_items", "the Ticket schema says when a reporting user gets item_id")

	list := doc.ops["GET /api/v1/tickets"]
	require.NotNil(t, list.op)
	var itemParam map[string]any
	for _, p := range list.op["parameters"].([]any) {
		if m, _ := p.(map[string]any); m["name"] == "item_id" {
			itemParam = m
		}
	}
	require.NotNil(t, itemParam)
	pd, _ := itemParam["description"].(string)
	require.Contains(t, pd, "403")
	require.Contains(t, pd, "users_show_items")
	resp403 := list.op["responses"].(map[string]any)["403"].(map[string]any)["description"].(string)
	require.Contains(t, resp403, "item_id is not available")

	fields := doc.ops["GET /api/v1/guest/fields"]
	fd, _ := fields.op["description"].(string)
	require.NotContains(t, fd, "picks a category and nothing below it")
	require.Contains(t, fd, "guests_show_types")
}
