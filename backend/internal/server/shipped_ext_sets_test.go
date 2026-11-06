package server

import (
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// The two lists of "the types this project ships" have to be the same list.
//
// allowedExt is what the handler stores a MIME type from and what shippedExt
// decides containment on; admin.DefaultAllowedTypes is what a fresh instance
// accepts and what TestShippedExtensions_AreNeverAContradictionOfThemselves
// walks. A comment said they must agree, which is not a thing a comment can
// do. If they drift apart, one of two failures follows and neither is loud:
// an extension in the defaults but not here is accepted by the allowlist and
// then refused for having no MIME type, and one here but not in the defaults
// is contained on instances that never asked for it.
func TestShippedExtensionSets_AreTheSameSet(t *testing.T) {
	inDefaults := map[string]bool{}
	for _, ext := range admin.DefaultAllowedTypes() {
		inDefaults[ext] = true
		if _, ok := allowedExt[ext]; !ok {
			t.Errorf("%s is accepted by a fresh instance but has no MIME type here, "+
				"so every upload of one is refused as an unsupported type", ext)
		}
	}
	for ext := range allowedExt {
		if !inDefaults[ext] {
			t.Errorf("%s has a MIME type here but is not in the defaults, so shippedExt "+
				"contains it on instances that never allowed it", ext)
		}
	}
}
