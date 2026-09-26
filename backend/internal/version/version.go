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
//
// Bumped when the beta branch was cut, for the same reason: it said 1.2.0 on
// a build that is not 1.2.0, so an operator running the beta could not tell
// from /api/v1/site which one they had. There is no 1.3.0 tag and no release;
// the suffix says so.
var Version = "1.3.0-beta"
