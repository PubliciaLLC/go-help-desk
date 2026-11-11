package server_test

import (
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// An upload that takes longer than the server's WriteTimeout must still be
// answered. net/http starts the write deadline when the request headers are
// read, so a body that trickles in for longer than WriteTimeout used to be
// stored and then answered on a dead connection: the client saw a network
// error for an upload that had already committed, and its retry made a
// duplicate. See bodyTransferTimeout.
//
// httptest's default server has no timeouts, so the real ones are scaled down
// (30s becomes 1s) on a server built for the purpose.
func TestUpload_SlowBodyOutlastingWriteTimeoutIsStillAnswered(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		Subject: "Slow upload", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	ts := httptest.NewUnstartedServer(h.srv)
	ts.Config.ReadTimeout = time.Second
	ts.Config.WriteTimeout = time.Second
	ts.Start()
	defer ts.Close()

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		fw, _ := mw.CreateFormFile("file", "slow.txt")
		_, _ = fw.Write([]byte("first half, "))
		time.Sleep(1500 * time.Millisecond) // past both server deadlines
		_, _ = fw.Write([]byte("second half"))
		_ = mw.Close()
		_ = pw.Close()
	}()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/tickets/"+tk.ID.String()+"/attachments", pr)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "ApiKey "+h.apiKey)

	res, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	require.NoError(t, err, "the upload committed; the answer must not be lost to the write deadline")
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)
}
