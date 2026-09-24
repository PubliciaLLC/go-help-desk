package server_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// A file named as text is stored as text, on a default instance, whatever is
// inside it.
//
// This is a regression test with four real files behind it. 1.2.0 accepted all
// four; the detection branch refused every one with 415 invalid_file on an
// instance that had changed no setting, because each holds at least one byte
// the detector treats as binary. The content came back as
// application/octet-stream with an *empty* extension, an empty extension was
// read as a mismatch, "" is on nobody's allowlist, and the default handling is
// refuse — so an ordinary log file was turned away by a control that exists to
// avoid firing on ordinary files.
//
// The rule these pin: rendering depends on the name, not on the content. A
// file called crash.log opens in a text editor whatever bytes it holds, so
// there is nothing for a wrap to contain — only something to say. A claimed
// text extension is therefore never refused and never wrapped. It can still be
// flagged, and the gzip row below is there so "never refused" does not quietly
// become "never noticed".
func TestUpload_AClaimedTextExtensionIsNeverRefusedOrWrapped(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// No setting is written. This is the instance that upgraded and touched
	// nothing, which is the instance the regression was found on.

	cases := []struct {
		name     string
		filename string
		content  []byte
		// wantMismatch is whether the row carries the flag. Every case here is
		// stored under its own name; what differs is whether there is anything
		// to say about it.
		wantMismatch bool
	}{
		{
			// A log from a process that died mid-write. The tail is NUL
			// padding, which is enough for the detector to give up entirely.
			name:         "a NUL-padded log from a crashed app",
			filename:     "crash.log",
			content:      append([]byte("2026-09-23 10:00:00 INFO started\n"), bytes.Repeat([]byte{0}, 64)...),
			wantMismatch: false,
		},
		{
			// Every second byte is zero, and without a BOM nothing says the
			// file is UTF-16. Notepad on Windows writes this.
			name:         "a UTF-16 .txt with no BOM",
			filename:     "notes.txt",
			content:      utf16LE("2026-09-23 10:00:00 INFO started\nsecond line here\n"),
			wantMismatch: false,
		},
		{
			// A rotated log compressed in place, name unchanged. The content
			// really is a gzip stream, so this one is a genuine contradiction
			// and is flagged — and still not refused, because the reader's
			// machine will open app.log as text regardless.
			name:         "a gzipped rotated log still named .log",
			filename:     "app.log",
			content:      gzipped("2026-09-23 10:00:00 INFO started\n"),
			wantMismatch: true,
		},
		{
			// The design's own canonical ordinary attachment, named as such in
			// #165 and in DESIGN.md: a captured HTTP response saved out of a
			// browser's devtools. The product refused its own documented
			// example.
			name:         "HTML named .log",
			filename:     "capture.log",
			content:      []byte("<!DOCTYPE html>\n<html><body><p>upstream returned this</p></body></html>\n"),
			wantMismatch: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
				Subject: tc.name, Description: "x", CategoryID: h.catID,
				ReporterUserID: &h.staffID,
			})
			require.NoError(t, err)

			res := uploadNamed(t, h, tk.ID.String(), tc.filename, tc.content)
			raw, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			res.Body.Close()
			require.Equal(t, http.StatusCreated, res.StatusCode,
				"1.2.0 accepted this file and a default instance still must; body: %s", raw)

			att := attachmentNamedAnyOf(t, h, tk.ID.String(), tc.filename)
			require.Equal(t, tc.filename, att.Filename,
				"a text-named file is stored under its own name, never as an archive of itself")
			require.NotNil(t, att.ContentMismatch,
				"nil means nobody looked, which is not true of a file we just inspected")
			require.Equal(t, tc.wantMismatch, *att.ContentMismatch)
		})
	}
}

// And the narrowness of that rule: under a claimed *binary* extension, content
// the detector cannot place is still a contradiction, and a default instance
// still refuses it.
//
// A PDF that cannot be identified as a PDF is worth saying something about.
// Without this, "the detector failing to place a file is not evidence of
// deception" widens into "an unidentifiable file is never anything", and the
// tier the setting governs loses half its input.
func TestUpload_UnidentifiableContentUnderABinaryNameIsStillRefused(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	id := ticketForUpload(t, h)

	res := uploadNamed(t, h, id, "report.pdf",
		append([]byte("2026-09-23 10:00:00 INFO started\n"), bytes.Repeat([]byte{0}, 64)...))
	raw, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	res.Body.Close()
	require.Equal(t, http.StatusUnsupportedMediaType, res.StatusCode, "body: %s", raw)

	var body struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	require.Equal(t, "invalid_file", body.Error.Code)

	assertUploadLeftNothingBehind(t, h, id)
}

// utf16LE encodes s as UTF-16 little-endian with no byte order mark, which is
// what makes it undetectable: the BOM is the only thing that would say what
// this is.
func utf16LE(s string) []byte {
	var b bytes.Buffer
	for _, r := range s {
		_ = binary.Write(&b, binary.LittleEndian, uint16(r))
	}
	return b.Bytes()
}

func gzipped(s string) []byte {
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	if _, err := zw.Write([]byte(s)); err != nil {
		panic(err)
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return b.Bytes()
}
