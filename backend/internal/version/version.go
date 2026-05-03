// Package version holds the application version string.
// Override at build time with:
//
//	go build -ldflags "-X github.com/publiciallc/go-help-desk/backend/internal/version.Version=1.2.3"
package version

// Version is the application version. Defaults to "0.1.0-dev" and is
// overridden by the CI release build via ldflags.
var Version = "1.0.1"
