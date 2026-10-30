// Package attachment holds the facts a help desk records about an uploaded
// file's content, as opposed to what the uploader claimed about it: what the
// bytes actually are, what they hash to, and how a file identified as
// malicious is wrapped before it is stored.
//
// Everything here is a pure function of a byte slice. There is no HTTP, no
// database and no filesystem: the handler decides what to do with these
// answers, and the store decides where to keep them.
package attachment

import (
	"errors"
	"mime"
	"strings"

	"github.com/gabriel-vasile/mimetype"
)

// errNotImplemented is the placeholder every function in this package returns
// until it is written. Functions with no error to return panic with the same
// words instead.
var errNotImplemented = errors.New("not implemented")

// Detect reports what the content of data is, independent of any filename.
//
// It returns the detector's own canonical extension for the format it
// recognised — with the leading dot, e.g. ".docx" — and the matching MIME
// type. Returning the extension is the point: it lets a claimed extension be
// compared with a detected one directly, with no table mapping one detector's
// MIME vocabulary onto another's.
//
// Only a prefix of data is read, so the cost does not grow with the file.
//
// There is no error to return. The detector never fails: content it cannot
// place is reported as application/octet-stream with an empty extension, which
// is an answer ("nothing recognisable") rather than a failure. IsMismatch
// turns that answer into a flag, so an unidentifiable file does not look like
// a verified one.
func Detect(data []byte) (ext string, mimeType string) {
	m := mimetype.Detect(data)

	// The bare media type. "text/html; charset=utf-8" and "text/html" are the
	// same answer, and the parameters are noise in a column the UI renders.
	media, _, err := mime.ParseMediaType(m.String())
	if err != nil {
		// Unreachable with this library, which builds its own strings, but a
		// malformed type must not become a stored value nothing can parse.
		media = "application/octet-stream"
	}

	// The library spells an unknown type's extension as "" already; this is
	// only about the leading dot and case, so the result can be compared with
	// filepath.Ext output directly.
	return strings.ToLower(m.Extension()), media
}
