package server_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/siteicon"
	"github.com/publiciallc/go-help-desk/backend/internal/server"
	"github.com/publiciallc/go-help-desk/backend/internal/ui"
)

// The app shell (#708): index.html is rendered per request, so the title, the
// icon links and the home screen name are right before any script runs.

const shellFixture = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <!-- ghd:head -->
    <link rel="icon" type="image/svg+xml" href="/favicon.svg" />
    <title>FROM-THE-VITE-FILE</title>
    <!-- /ghd:head -->
    <script type="module" src="/assets/app.js"></script>
  </head>
  <body><div id="root"></div></body>
</html>
`

func shellFS(index string) fstest.MapFS {
	return fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte(index)},
		"assets/app.js": &fstest.MapFile{Data: []byte("export {}")},
	}
}

// shellWith is the fixture with its marked region replaced by block.
func shellWith(block string) string {
	i := strings.Index(shellFixture, "<!-- ghd:head -->")
	j := strings.Index(shellFixture, "<!-- /ghd:head -->") + len("<!-- /ghd:head -->")
	return shellFixture[:i] + block + shellFixture[j:]
}

const defaultBlock = `<!-- ghd:head -->
    <link rel="icon" type="image/svg+xml" href="/favicon.svg" />
    <link rel="manifest" href="/api/v1/branding/manifest.webmanifest" />
    <meta name="apple-mobile-web-app-title" content="Go Help Desk" />
    <title>Go Help Desk</title>
    <!-- /ghd:head -->`

func getShell(h http.Handler, path string) (*http.Response, string) {
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
	res := rr.Result()
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res, string(b)
}

func TestShell_DefaultBlock(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	got := h.srv.ShellHead(context.Background())
	require.Equal(t, defaultBlock, got)

	_, body := getShell(server.NewSPAHandler(shellFS(shellFixture), h.srv.ShellHead), "/")
	require.Equal(t, shellWith(defaultBlock), body, "byte for byte, and nothing of the file's own block survives")
	require.NotContains(t, body, "FROM-THE-VITE-FILE")
}

func TestShell_WithAnIcon(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	require.NoError(t, h.adminSvc.SetString(context.Background(), "site_name", "Acme Help"))
	require.NoError(t, h.adminSvc.SetString(context.Background(), "site_short_name", "Acme"))
	st := readStatus(t, h.uploadIcon(t, newIconUpload(iconPNG(t, iconDisc(600))).withBackground("#102030")))

	want := `<!-- ghd:head -->
    <link rel="icon" href="/api/v1/branding/icons/favicon.ico?v=` + st.Version + `" sizes="16x16 32x32 48x48" />
    <link rel="icon" type="image/png" sizes="32x32" href="/api/v1/branding/icons/favicon-32.png?v=` + st.Version + `" />
    <link rel="icon" type="image/png" sizes="16x16" href="/api/v1/branding/icons/favicon-16.png?v=` + st.Version + `" />
    <link rel="apple-touch-icon" sizes="180x180" href="/api/v1/branding/icons/apple-touch-icon.png?v=` + st.Version + `" />
    <link rel="manifest" href="/api/v1/branding/manifest.webmanifest" />
    <meta name="theme-color" content="#102030" />
    <meta name="apple-mobile-web-app-title" content="Acme" />
    <title>Acme Help</title>
    <!-- /ghd:head -->`
	require.Equal(t, want, h.srv.ShellHead(context.Background()))

	_, body := getShell(server.NewSPAHandler(shellFS(shellFixture), h.srv.ShellHead), "/")
	require.Equal(t, shellWith(want), body)
	require.NotContains(t, body, `href="/favicon.svg"`, "the built-in icon link is replaced, not added to")
}

// The names are an administrator's text and land in HTML read by every browser
// that visits: they are escaped, never interpreted.
func TestShell_HostileNamesAreEscaped(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetString(ctx, "site_name", `<script>alert(1)</script>&"`))
	require.NoError(t, h.adminSvc.SetString(ctx, "site_short_name", `"><script>alert(2)</script>`))

	for _, withIcon := range []bool{false, true} {
		if withIcon {
			readStatus(t, h.uploadIcon(t, newIconUpload(iconPNG(t, iconDisc(600)))))
		}
		_, body := getShell(server.NewSPAHandler(shellFS(shellFixture), h.srv.ShellHead), "/")

		require.NotContains(t, body, "<script>alert", "markup in a name never reaches the page as markup")
		require.NotContains(t, body, `"><script`)
		require.Contains(t, body, `<title>&lt;script&gt;alert(1)&lt;/script&gt;&amp;&#34;</title>`)
		require.Contains(t, body, `<meta name="apple-mobile-web-app-title" content="&#34;&gt;&lt;script&gt;alert(2)&lt;/script&gt;" />`)
		// The one script tag is the file's own.
		require.Equal(t, 1, strings.Count(body, "<script"), "withIcon=%v", withIcon)
	}
}

func TestShell_WhichResponsesAreRendered(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	handler := server.NewSPAHandler(shellFS(shellFixture), h.srv.ShellHead)
	want := shellWith(defaultBlock)

	for _, path := range []string{"/", "/index.html", "/tickets/123", "/admin/settings/branding", "/does/not/exist"} {
		t.Run(path, func(t *testing.T) {
			res, body := getShell(handler, path)
			require.Equal(t, http.StatusOK, res.StatusCode, "the shell itself, not a redirect or a 404")
			require.Equal(t, want, body)
			require.Equal(t, "text/html; charset=utf-8", res.Header.Get("Content-Type"))
			require.Equal(t, "no-cache", res.Header.Get("Cache-Control"),
				"the icon can change at any time, so the shell is revalidated")
			// Every page the script runs on carries the headers (see security_headers_everywhere_test.go).
			require.NotEmpty(t, res.Header.Get("Content-Security-Policy"))
			require.Equal(t, "DENY", res.Header.Get("X-Frame-Options"))
			require.Equal(t, "nosniff", res.Header.Get("X-Content-Type-Options"))
		})
	}

	t.Run("a static asset is the file", func(t *testing.T) {
		res, body := getShell(handler, "/assets/app.js")
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Equal(t, "export {}", body)
		require.NotContains(t, res.Header.Get("Content-Type"), "text/html")
	})
	t.Run("the API is not the shell", func(t *testing.T) {
		for _, p := range []string{"/api/v1/nothing", "/mcp/x"} {
			res, _ := getShell(handler, p)
			require.Equal(t, http.StatusNotFound, res.StatusCode, p)
		}
	})
}

// A file without the markers is served as it is: the placeholder in a fresh
// checkout, or a build that dropped the comments. Fails safe to the old
// behaviour, never to a broken page.
func TestShell_WithoutMarkersIsServedUnchanged(t *testing.T) {
	head := func(context.Context) string { return "HEAD-MUST-NOT-APPEAR" }
	cases := map[string]string{
		"no markers":     "<!doctype html><title>Go Help Desk</title><p>Frontend not built.</p>",
		"only the open":  "<html><!-- ghd:head --><title>x</title></html>",
		"only the close": "<html><title>x</title><!-- /ghd:head --></html>",
		"out of order":   "<html><!-- /ghd:head --><title>x</title><!-- ghd:head --></html>",
		"open twice":     "<html><!-- ghd:head --><!-- ghd:head --><title>x</title><!-- /ghd:head --></html>",
		"close twice":    "<html><!-- ghd:head --><title>x</title><!-- /ghd:head --><!-- /ghd:head --></html>",
		"a spacing typo": "<html><!--ghd:head--><title>x</title><!--/ghd:head--></html>",
	}
	for name, index := range cases {
		t.Run(name, func(t *testing.T) {
			handler := server.NewSPAHandler(shellFS(index), head)
			for _, p := range []string{"/", "/tickets/1"} {
				res, body := getShell(handler, p)
				require.Equal(t, http.StatusOK, res.StatusCode)
				require.Equal(t, index, body, p)
				require.NotContains(t, body, "HEAD-MUST-NOT-APPEAR")
			}
		})
	}

	t.Run("no head function", func(t *testing.T) {
		_, body := getShell(server.NewSPAHandler(shellFS(shellFixture), nil), "/")
		require.Equal(t, shellFixture, body)
	})
	t.Run("no index.html at all", func(t *testing.T) {
		res, _ := getShell(server.NewSPAHandler(fstest.MapFS{"assets/app.js": &fstest.MapFile{Data: []byte("x")}}, head), "/tickets/1")
		require.Equal(t, http.StatusNotFound, res.StatusCode)
	})
}

// A store that cannot be read never breaks the page: the shell falls back to the
// built-in block, as the manifest falls back to the default.
type failingIconStore struct{ siteicon.Store }

func (failingIconStore) Get(context.Context) (siteicon.Icon, bool, error) {
	return siteicon.Icon{}, false, errors.New("injected icon read failure")
}

func TestShell_StoreErrorFallsBackToTheDefaultBlock(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	readStatus(t, h.uploadIcon(t, newIconUpload(iconPNG(t, iconDisc(600)))))
	require.Contains(t, h.srv.ShellHead(context.Background()), "apple-touch-icon", "the icon is in force before the fault")

	server.WithSiteIcons(failingIconStore{})(h.srv)
	require.Equal(t, defaultBlock, h.srv.ShellHead(context.Background()))
	_, body := getShell(server.NewSPAHandler(shellFS(shellFixture), h.srv.ShellHead), "/")
	require.Equal(t, shellWith(defaultBlock), body)

	// And the manifest, which never answers 500.
	res := h.publicGet("/api/v1/branding/manifest.webmanifest")
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	m := h.manifest(t)
	require.Len(t, m.Icons, 1)
	require.Equal(t, "/favicon.svg", m.Icons[0].Src)
}

// Without the option at all (a partial server): no icon routes, the default
// shell and manifest, "not set" for the admin, and a refusal for a write.
func TestSiteIcon_WithoutAStore(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	server.WithSiteIcons(nil)(h.srv)

	require.Equal(t, defaultBlock, h.srv.ShellHead(context.Background()))
	res := h.publicGet("/api/v1/branding/icons/icon-192.png")
	res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode)
	require.Len(t, h.manifest(t).Icons, 1)
	require.False(t, h.iconStatus(t).Set)

	status, code, _ := readAPIError(t, h.uploadIcon(t, newIconUpload(iconPNG(t, iconDisc(600)))))
	require.Equal(t, http.StatusInternalServerError, status)
	require.Equal(t, "storage_error", code)
	res = h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings/icon", map[string]string{"background": "#000000"})
	res.Body.Close()
	require.Equal(t, http.StatusInternalServerError, res.StatusCode)
	res = h.doAsAdmin(t, http.MethodDelete, "/api/v1/admin/settings/icon", nil)
	res.Body.Close()
	require.Equal(t, http.StatusInternalServerError, res.StatusCode)
}

// The built frontend carries the markers, or the server would silently serve
// the default icon forever. Skipped in a checkout where nothing was built (the
// placeholder); CI restores the real dist, where this runs.
func TestBuiltIndexHasMarkers(t *testing.T) {
	b, err := fs.ReadFile(ui.FS(), "index.html")
	require.NoError(t, err)
	if strings.Contains(string(b), "Frontend not built") {
		t.Skip("the frontend is not built in this checkout")
	}
	s := string(b)
	require.Equal(t, 1, strings.Count(s, "<!-- ghd:head -->"))
	require.Equal(t, 1, strings.Count(s, "<!-- /ghd:head -->"))
	require.Less(t, strings.Index(s, "<!-- ghd:head -->"), strings.Index(s, "<!-- /ghd:head -->"))
}
