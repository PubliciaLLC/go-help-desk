package server

import (
	"io/fs"
	"net/http"
	"strings"
)

// SPAHandler serves a React SPA from an fs.FS. Any path that does not match a
// known file falls back to index.html so that client-side routing works.
type SPAHandler struct {
	fs   fs.FS
	root http.Handler
}

// NewSPAHandler returns an http.Handler that serves static files from fsys and
// falls back to index.html for all unmatched paths.
// NewSPAHandler serves the built frontend, with the same security headers
// every other response carries.
//
// The headers are applied here rather than left to the caller because this
// handler is mounted on the bare ServeMux, outside the chi chain that sets
// them for /api/. The result was that the pages which actually run script in
// a browser — the whole UI, including every admin screen — were the only ones
// served with no Content-Security-Policy, no X-Frame-Options and no nosniff,
// while the JSON endpoints had all three. That is the wrong way round: CSP on
// a JSON response does very little, and on the HTML it is the backstop for
// exactly the kind of injected script the rest of this codebase works to
// prevent.
func NewSPAHandler(fsys fs.FS) http.Handler {
	return securityHeaders(&SPAHandler{
		fs:   fsys,
		root: http.FileServer(http.FS(fsys)),
	})
}

func (s *SPAHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Never serve the API or MCP paths from the SPA handler.
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/mcp/") {
		http.NotFound(w, r)
		return
	}

	// Check whether the file exists in the embedded FS.
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if _, err := fs.Stat(s.fs, path); err != nil {
		// File not found → serve index.html for client-side routing.
		r = r.Clone(r.Context())
		r.URL.Path = "/"
		http.ServeFileFS(w, r, s.fs, "index.html")
		return
	}
	s.root.ServeHTTP(w, r)
}
