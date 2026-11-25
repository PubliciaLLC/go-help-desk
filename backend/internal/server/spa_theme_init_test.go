package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/server"
)

// /theme-init.js sets the theme before first paint (#709). The CSP is
// script-src 'self', so it is a file served by the application itself, not an
// inline script, and it is what the browser blocks parsing on: it has to come
// back as JavaScript, from this origin, under the same policy as everything
// else, and a change to it has to reach people at the next upgrade.
func TestSPAHandler_ServesThemeInit(t *testing.T) {
	const script = "(function(){document.documentElement.classList.add('dark')})()"
	ui := fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<!doctype html><title>x</title>")},
		"theme-init.js": &fstest.MapFile{Data: []byte(script)},
	}
	h := server.NewSPAHandler(ui)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/theme-init.js", nil))
	res := rr.Result()
	defer res.Body.Close()

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, script, rr.Body.String(), "the file itself, not index.html")
	require.True(t, strings.HasPrefix(res.Header.Get("Content-Type"), "text/javascript"),
		"a script served as anything else is refused under nosniff: %q", res.Header.Get("Content-Type"))
	require.Equal(t, "nosniff", res.Header.Get("X-Content-Type-Options"))
	require.Contains(t, res.Header.Get("Content-Security-Policy"), "script-src 'self'")
	// The file is not hashed into the build, so a long cache would keep an old
	// script after an upgrade.
	require.Equal(t, "no-cache", res.Header.Get("Cache-Control"))
}

// Without the file (a build that lacks it) the answer is a 404, never
// index.html: the browser would otherwise run a page of HTML as a script, and
// the console would say a syntax error where the truth is "missing file".
func TestSPAHandler_MissingThemeInitIsNotFound(t *testing.T) {
	ui := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>x</title>")},
	}
	h := server.NewSPAHandler(ui)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/theme-init.js", nil))
	require.Equal(t, http.StatusNotFound, rr.Code)
	require.NotContains(t, rr.Body.String(), "<!doctype")
}

// The other files are served as they were: only this one has its own caching.
func TestSPAHandler_OtherFilesKeepTheirHeaders(t *testing.T) {
	ui := fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<!doctype html>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("export {}")},
	}
	h := server.NewSPAHandler(ui)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
	require.Equal(t, http.StatusOK, rr.Code)
	require.Empty(t, rr.Header().Get("Cache-Control"))
}
