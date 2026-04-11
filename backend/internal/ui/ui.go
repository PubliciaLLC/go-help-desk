// Package ui embeds the compiled React SPA from ../../frontend/dist.
// The dist directory is populated by `npm run build` in the frontend directory
// before the Go binary is compiled (handled by the Dockerfile).
package ui

import (
	"embed"
	"io/fs"
)

//go:embed dist
var embeddedFS embed.FS

// FS returns the embedded frontend dist as a sub-filesystem rooted at "dist".
func FS() fs.FS {
	sub, err := fs.Sub(embeddedFS, "dist")
	if err != nil {
		panic("ui: failed to create sub-FS: " + err.Error())
	}
	return sub
}
