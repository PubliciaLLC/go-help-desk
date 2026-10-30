package server_test

import (
	"bytes"
	"compress/gzip"
	"image"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/attachment"
)

// Every extension this project ships must survive its own containment rule.
//
// Containment fires when the content contradicts the name, and it applies only
// to the nine we ship — on the argument that their spellings were checked
// against the detector. That argument is not quite the invariant, and the
// difference matters: the detector calls a .log a .txt and a .jpeg a .jpg, so
// two of the nine are not its spelling at all. They survive because
// IsMismatch's relaxations cover them, not because the names agree.
//
// So the real invariant is "every shipped extension is either the detector's
// own spelling or covered by a relaxation", and nothing pinned it. Add .tif to
// the defaults in some future release — an entirely reasonable thing to do,
// since the detector knows TIFF perfectly well — and every genuine TIFF is
// contained and refused, which is precisely the bug that was just fixed for
// operator-added extensions, reintroduced silently for a shipped one.
//
// This walks the shipped list itself rather than a copy, so an extension added
// without a fixture fails here rather than in production.
func TestShippedExtensions_AreNeverAContradictionOfThemselves(t *testing.T) {
	fixtures := map[string][]byte{
		".pdf":  []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\ntrailer\n<< /Root 1 0 R >>\n%%EOF\n"),
		".txt":  []byte("an ordinary note, several words long so nothing sniffs oddly\n"),
		".log":  []byte("2026-09-24 07:00:00 INFO started\n2026-09-24 07:00:01 INFO ready\n"),
		".png":  encodePNG(),
		".bmp":  encodeBMP(),
		".jpg":  encodeJPEG(),
		".jpeg": encodeJPEG(),
		".docx": detDOCX(t),
		".xlsx": detXLSX(t),
	}

	for _, ext := range admin.DefaultAllowedTypes() {
		t.Run(ext, func(t *testing.T) {
			content, ok := fixtures[ext]
			require.True(t, ok,
				"%s ships as an accepted type and has no fixture here, so nothing checks "+
					"that a genuine one survives containment", ext)

			detectedExt, detectedMIME := attachment.Detect(content)
			require.False(t, attachment.IsMismatch(ext, detectedExt, detectedMIME),
				"a genuine %s file is reported as contradicting its own name "+
					"(detector says %q / %s). It would be contained and, on a default "+
					"instance, refused.", ext, detectedExt, detectedMIME)
		})
	}
}

// And a file that really is gzip under a shipped name still contradicts it —
// or the test above could pass by the rule never firing at all.
func TestShippedExtensions_StillCatchARealContradiction(t *testing.T) {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte("not a pdf at all\n"))
	require.NoError(t, zw.Close())

	detectedExt, detectedMIME := attachment.Detect(gz.Bytes())
	require.True(t, attachment.IsMismatch(".pdf", detectedExt, detectedMIME),
		"gzip named .pdf is a contradiction and must still be caught")
}

func encodePNG() []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 8, 8)))
	return b.Bytes()
}

func encodeJPEG() []byte {
	var b bytes.Buffer
	_ = jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil)
	return b.Bytes()
}

// A minimal BMP header plus pixel data; the detector reads the signature.
func encodeBMP() []byte {
	b := make([]byte, 64)
	copy(b, []byte{'B', 'M'})
	b[10] = 54
	b[14] = 40
	return b
}
