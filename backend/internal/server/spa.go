package server

import (
	"bytes"
	"context"
	"io/fs"
	"net/http"
	"strings"
)

// The region of index.html the server rewrites on every request (#708): the
// tags that name the page, its icons and its home screen name. The frontend
// build keeps both comments; what stands between them in the file is what the
// Vite dev server shows, and is never served as it is.
const (
	headOpen  = "<!-- ghd:head -->"
	headClose = "<!-- /ghd:head -->"
)

// SPAHandler serves a React SPA from an fs.FS. Any path that does not match a
// known file falls back to index.html so that client-side routing works.
type SPAHandler struct {
	fs   fs.FS
	root http.Handler

	// index is index.html split around the marked region, read once. Nil when
	// the file is missing, has no markers or has them wrong, or when no head
	// function was given; every response is then the file as it is, which is
	// what this handler did before the markers existed.
	index *shellIndex
	head  func(context.Context) string
}

// shellIndex is index.html with the marked region (the markers included) cut out.
type shellIndex struct{ before, after []byte }

func splitShell(index []byte) *shellIndex {
	o, c := bytes.Index(index, []byte(headOpen)), bytes.Index(index, []byte(headClose))
	if o < 0 || c < o ||
		bytes.Count(index, []byte(headOpen)) != 1 || bytes.Count(index, []byte(headClose)) != 1 {
		return nil
	}
	return &shellIndex{before: index[:o], after: index[c+len(headClose):]}
}

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
//
// head returns the block that replaces the marked region of index.html on each
// request (Server.ShellHead); it begins and ends with the markers. The page is
// then served as no-cache HTML: the icon can change at any moment, and a shell
// that came from a cache would name the old one.
func NewSPAHandler(fsys fs.FS, head func(context.Context) string) http.Handler {
	s := &SPAHandler{fs: fsys, root: http.FileServer(http.FS(fsys)), head: head}
	if b, err := fs.ReadFile(fsys, "index.html"); err == nil && head != nil {
		s.index = splitShell(b)
	}
	return securityHeaders(s)
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
	_, err := fs.Stat(s.fs, path)
	if s.index != nil && (path == "index.html" || err != nil) {
		// Every response whose body is index.html: the root, the file by its
		// name (rendered, not FileServer's redirect to ./) and the fallback
		// for a client-side route.
		s.serveShell(w, r)
		return
	}
	if err != nil {
		// File not found → serve index.html for client-side routing.
		r = r.Clone(r.Context())
		r.URL.Path = "/"
		http.ServeFileFS(w, r, s.fs, "index.html")
		return
	}
	s.root.ServeHTTP(w, r)
}

func (s *SPAHandler) serveShell(w http.ResponseWriter, r *http.Request) {
	block := s.head(r.Context())
	body := make([]byte, 0, len(s.index.before)+len(block)+len(s.index.after))
	body = append(body, s.index.before...)
	body = append(body, block...)
	body = append(body, s.index.after...)

	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	_, _ = w.Write(body)
}
