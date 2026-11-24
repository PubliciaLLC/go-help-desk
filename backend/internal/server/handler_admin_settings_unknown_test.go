package server_test

import (
	"context"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// PATCH /admin/settings refuses a key it does not know, whole body and
// nothing written (#421). A typo used to answer 204, leave a row nothing
// reads, and leave the real setting as it was.

func TestUpdateSettings_UnknownKeyRefusesTheWholeBody(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
		key  string // the key the message must name
	}{
		{"one unknown key", map[string]any{"mfa_enabld": true}, "mfa_enabld"},
		{"unknown beside a valid one", map[string]any{"site_name": "Renamed", "bogus_setting": 1}, "bogus_setting"},
		{"the dump's presence flag is not a setting", map[string]any{"site_name": "Renamed", "oidc_client_secret_set": true}, "oidc_client_secret_set"},
		{"case matters", map[string]any{"Site_Name": "x"}, "Site_Name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			ctx := context.Background()
			before, err := h.adminSvc.ListAll(ctx)
			require.NoError(t, err)

			res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings", tc.body)
			var e struct {
				Error struct{ Code, Message string } `json:"error"`
			}
			decodeJSON(t, res, &e)
			require.Equal(t, http.StatusBadRequest, res.StatusCode)
			// readonly_setting is the existing, more specific refusal for
			// the "_set" flags and stays so.
			if tc.key != "oidc_client_secret_set" {
				require.Equal(t, "unknown_setting", e.Error.Code)
			}
			require.Contains(t, e.Error.Message, tc.key)

			after, err := h.adminSvc.ListAll(ctx)
			require.NoError(t, err)
			require.Equal(t, before, after, "a refused body must write nothing, valid keys included")
		})
	}
}

// A machine credential is asked about the key first: an unknown key is 400,
// and an auth-critical one is still 403, as before.
func TestUpdateSettings_UnknownKeyFromAMachineCredential(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{"nonsense": true})
	res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)

	res = h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{"mfa_enabled": true})
	res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode)
}

// Known keys are unaffected.
func TestUpdateSettings_KnownKeysStillWrite(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{"site_name": "Renamed", "sla_enabled": true, "ticket_prefix": "ABC"})
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	v, err := h.adminSvc.GetString(context.Background(), admin.KeySiteName)
	require.NoError(t, err)
	require.Equal(t, "Renamed", v)
}

// settingsPageSources reads every non-test source under
// frontend/src/pages/admin/settings (the pages and their sections) and returns
// them as one string.
func settingsPageSources(t *testing.T) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	require.True(t, ok)
	// backend/internal/server/<this file> -> repository root
	root := filepath.Join(filepath.Dir(here), "..", "..", "..", "frontend", "src", "pages", "admin", "settings")
	var sb strings.Builder
	n := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() || strings.Contains(name, ".test.") || !(strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".tsx")) {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sb.Write(b)
		sb.WriteByte('\n')
		n++
		return nil
	})
	require.NoError(t, err, "this test reads the settings pages from the checkout")
	require.NotZero(t, n, "no settings page sources under %s", root)
	return sb.String()
}

// The admin settings pages are the one client that PATCHes this route. Every
// key they can send must be known, or saving a field on a page would 400.
// Read from the pages' source (pages and sections): a vitest would only see
// the keys of the test that happens to run.
func TestUpdateSettings_EveryKeyTheSettingsPageSendsIsKnown(t *testing.T) {
	src := settingsPageSources(t)
	// draft.bool('k'), setStr('k', ...), has('k'), toggleStrArr('k', ...),
	// secret('k'); the enabledSetting / keySetting entries of the reputation
	// table; and the key: entries of the staff email override table.
	accessor := regexp.MustCompile(`\b(?:setBool|setNum|setStr|setStrArr|toggleStrArr|bool|num|str|strArr|has|secret|setSecret|secretCleared|setSecretCleared)\(\s*'([a-z][a-z0-9_]*)'`)
	table := regexp.MustCompile(`\b(?:enabledSetting|keySetting|key):\s*'([a-z][a-z0-9_]*)'`)
	keys := map[string]bool{}
	for _, re := range []*regexp.Regexp{accessor, table} {
		for _, m := range re.FindAllStringSubmatch(src, -1) {
			keys[m[1]] = true
		}
	}
	require.Greater(t, len(keys), 15, "the pages' keys were not found; have the pages been restructured?")
	var unknown []string
	for k := range keys {
		if !admin.IsKnownKey(k) {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	require.Empty(t, unknown, "the Settings page sends keys the server would refuse")
}

// Whatever a migration seeds is a setting the server already holds, so a
// client echoing it back must not be told it is unknown.
func TestUpdateSettings_EveryMigrationSeededKeyIsKnown(t *testing.T) {
	_, here, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(here), "..", "database", "migrations")
	files, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	require.NoError(t, err)
	require.NotEmpty(t, files)

	insert := regexp.MustCompile(`(?is)INSERT\s+INTO\s+settings\b(.*?);`)
	tuple := regexp.MustCompile(`\(\s*'([a-z][a-z0-9_]*)'\s*,`)
	seen := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		for _, stmt := range insert.FindAllStringSubmatch(string(b), -1) {
			for _, m := range tuple.FindAllStringSubmatch(stmt[1], -1) {
				seen++
				require.True(t, admin.IsKnownKey(m[1]), "%s seeds %q, which PATCH /admin/settings would refuse", filepath.Base(f), m[1])
			}
		}
	}
	require.Greater(t, seen, 10, "no seeded keys were found; has the migration format changed?")
}
