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
func NewSPAHandler(fsys fs.FS) http.Handler {
	return &SPAHandler{
		fs:   fsys,
		root: http.FileServer(http.FS(fsys)),
	}
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
