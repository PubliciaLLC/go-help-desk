// Package version holds the application version string.
// Override at build time with:
//
//	go build -ldflags "-X github.com/publiciallc/go-help-desk/backend/internal/version.Version=1.2.3"
package version

// Version is the application version, reported in the UI footer and by
// GET /api/v1/site.
//
// Keep this in step with the release tag. It is the value that actually
// ships: nothing overrides it at build time — not backend/Dockerfile, not the
// CI image job — despite the comment above describing an ldflags override and
// the old comment here claiming "overridden by the CI release build".
//
// v1.1.0 shipped reporting itself as 1.0.1 because this constant was not
// bumped and the described override does not exist. A test pins the two
// together so the next release cannot repeat it.
var Version = "1.1.1"
