package server_test

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// An attachment may be downloaded. It may not be rendered.
//
// Content-Type and Content-Disposition say so, and a browser listens when it
// is navigating. It does not listen for a subresource: <img src>, a CSS
// url(), <video>, <object> — those fetch the bytes and render them on this
// origin whatever the headers say. That is stored XSS territory for anything
// a browser will execute, and it is reached from our own pages, which is why
// a frontend test was standing in for a control. This is the control.
//
// Sec-Fetch-Dest is filled in by the browser and page script cannot set it,
// so it is a fact about the request rather than a claim by the requester.
func TestAttachmentDownload_RefusesToBeRendered(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "Fetch dest", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	// A real image, because this is the case that matters: a browser will
	// happily render an uploaded PNG from <img src> whatever headers come
	// back with it.
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	res := uploadNamed(t, h, tk.ID.String(), "logo.png", buf.Bytes())
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	id := attachmentIDNamed(t, h, tk.ID.String(), "logo.png")
	url := "/api/v1/tickets/" + tk.ID.String() + "/attachments/" + id

	// Every destination a browser uses when it is going to render what comes
	// back, including the ones behind a CSS url() (image, font) and the ones
	// that would run it (script, style).
	rendering := []string{
		"image", "iframe", "frame", "object", "embed",
		"video", "audio", "track", "script", "style", "font",
		"manifest", "worker", "sharedworker", "serviceworker",
	}
	for _, dest := range rendering {
		t.Run("refused for "+dest, func(t *testing.T) {
			res := h.doWithHeaders(t, http.MethodGet, url, nil,
				map[string]string{"Sec-Fetch-Dest": dest})
			defer res.Body.Close()
			require.Equal(t, http.StatusForbidden, res.StatusCode,
				"a %s request would render the attachment on this origin", dest)
		})
	}

	// A destination nobody has invented yet is refused too. This is the whole
	// reason it is an allow list: a block list is a promise to keep editing it.
	t.Run("refused for an unknown destination", func(t *testing.T) {
		res := h.doWithHeaders(t, http.MethodGet, url, nil,
			map[string]string{"Sec-Fetch-Dest": "somethingnew"})
		defer res.Body.Close()
		require.Equal(t, http.StatusForbidden, res.StatusCode)
	})

	// The check is per request; the response is cacheable for an hour and a
	// browser cache is keyed by URL. Without naming this header, a URL
	// fetched once in a way the check allows is then served to an <img> from
	// cache and the server never sees the second request — measured in
	// Chrome, where the image loaded and no request arrived. Naming it makes
	// a different Sec-Fetch-Dest a different cache entry.
	t.Run("the cache cannot serve a rendering request from an allowed one", func(t *testing.T) {
		res := h.doWithHeaders(t, http.MethodGet, url, nil,
			map[string]string{"Sec-Fetch-Dest": "document"})
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Contains(t, res.Header.Values("Vary"), "Sec-Fetch-Dest",
			"a cached response must not be reusable for a different destination")
	})

	// And the ways an attachment is actually fetched still work. "document" is
	// a link click or an <a download>; "empty" is fetch or XHR; absent is an
	// older browser or any command-line client, none of which has a renderer
	// to protect.
	for _, dest := range []string{"document", "empty", ""} {
		name := dest
		if name == "" {
			name = "no header at all"
		}
		t.Run("allowed for "+name, func(t *testing.T) {
			headers := map[string]string{}
			if dest != "" {
				headers["Sec-Fetch-Dest"] = dest
			}
			res := h.doWithHeaders(t, http.MethodGet, url, nil, headers)
			defer res.Body.Close()
			require.Equal(t, http.StatusOK, res.StatusCode,
				"this is how an attachment is downloaded; it must keep working")
		})
	}
}

// doWithHeaders is h.do with extra request headers. Local to this file because
// Sec-Fetch-Dest is the only thing that needs it.
func (h *harness) doWithHeaders(t *testing.T, method, path string, body any, headers map[string]string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "ApiKey "+h.apiKey)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	h.srv.ServeHTTP(rr, req)
	return rr.Result()
}
