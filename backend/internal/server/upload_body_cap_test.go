package server_test

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// An oversized upload is refused without being read to the end.
//
// ParseMultipartForm's argument is a memory limit, not a body limit:
// everything past it spills to a temp file with no ceiling. So a 120 MB body
// was read to completion and written to disk in full, and only then answered
// 413 — any authenticated user could run several at once and fill the
// container's writable layer, bounded only by how fast they could push bytes.
//
// The test measures how much of the body the server took. That is the
// property, not the status code: answering 413 after swallowing 120 MB is the
// bug, and a test that only checked the status would have passed against it.
func TestUpload_AnOversizedBodyIsRefusedWithoutBeingSwallowed(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "Too big", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	// 120 MB of body, generated as it is read rather than held in memory —
	// building the whole thing would make this test cost what the bug did.
	const bodyBytes = 120 << 20

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	counted := &countingReader{r: pr}

	go func() {
		part, err := mw.CreateFormFile("file", "huge.pdf")
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		chunk := bytes.Repeat([]byte("A"), 1<<20)
		for written := 0; written < bodyBytes; written += len(chunk) {
			if _, err := part.Write(chunk); err != nil {
				// The server stopped reading. That is the pass condition,
				// not a failure.
				_ = pw.CloseWithError(err)
				return
			}
		}
		_ = mw.Close()
		_ = pw.Close()
	}()

	// A real HTTP server, not the recorder the other tests use: this is about
	// what happens on the wire, and a recorder has no wire.
	ts := httptest.NewServer(h.srv)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodPost,
		ts.URL+"/api/v1/tickets/"+tk.ID.String()+"/attachments", counted)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "ApiKey "+h.apiKey)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	require.Equal(t, http.StatusRequestEntityTooLarge, res.StatusCode)

	// The cap is 25 MB plus a megabyte of multipart framing. Allowing double
	// that leaves room for the client's in-flight buffering without leaving
	// room for the defect, which read every one of the 120 MB.
	const ceiling = 2 * (attachMaxBytesForTest + (1 << 20))
	require.Less(t, counted.n(), int64(ceiling),
		"the server read %d bytes of a body it was always going to refuse", counted.n())
}

// attachMaxBytesForTest mirrors the handler's own limit. Duplicated rather
// than exported: the constant is an implementation detail, and a test that
// forced it into the package's API would be a worse trade than one number.
const attachMaxBytesForTest = 25 << 20

type countingReader struct {
	r     io.Reader
	count int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.count += int64(n)
	return n, err
}

func (c *countingReader) n() int64 { return c.count }
