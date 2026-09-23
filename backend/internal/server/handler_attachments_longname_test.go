package server_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// A filename long enough to overflow a ZIP header field is refused at the
// door, not discovered at the end.
//
// The ZIP entry-name length is 16 bits. Over 65,535 bytes the writer this
// project wraps with truncates that field rather than refusing, which produced
// an archive no tool could open — accepted with a 201, written to disk, and
// listed on the ticket as a file nobody could ever read back. Any
// authenticated user could do it: the multipart reader allows a 10 MB part
// header and nothing between the UTF-8 check and the wrap bounded the name.
//
// 255 bytes is what almost every filesystem the download lands on imposes
// anyway, so this refuses at upload what the reader's own machine would refuse
// at the end.
func TestUpload_RefusesAFilenameNoArchiveCouldHold(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "Long name", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	// HTML content, so this would have taken the wrapping path — which is
	// where the corruption happened.
	html := []byte("<html><body>x</body></html>")

	t.Run("far past what a zip header can hold", func(t *testing.T) {
		name := strings.Repeat("a", 70000) + ".txt"
		res := uploadNamed(t, h, tk.ID.String(), name, html)
		defer res.Body.Close()
		require.Equal(t, http.StatusBadRequest, res.StatusCode,
			"this used to be accepted and stored as an unopenable archive")
	})

	t.Run("just over the cap", func(t *testing.T) {
		name := strings.Repeat("b", 252) + ".txt" // 256 bytes
		res := uploadNamed(t, h, tk.ID.String(), name, html)
		defer res.Body.Close()
		require.Equal(t, http.StatusBadRequest, res.StatusCode)
	})

	t.Run("exactly at the cap is still accepted", func(t *testing.T) {
		// The boundary in the other direction, so the cap cannot be "refuse
		// anything interesting" and pass.
		name := strings.Repeat("c", 251) + ".txt" // 255 bytes
		res := uploadNamed(t, h, tk.ID.String(), name, html)
		defer res.Body.Close()
		require.Equal(t, http.StatusCreated, res.StatusCode,
			"255 bytes is the limit, not the first refusal")
	})

	assertUploadLeftNothingBehindExceptAccepted(t, h, tk.ID.String(), 1)
}

// assertUploadLeftNothingBehindExceptAccepted checks the refused uploads wrote
// nothing, allowing for the one that was meant to succeed.
func assertUploadLeftNothingBehindExceptAccepted(t *testing.T, h *harness, ticketID string, want int) {
	t.Helper()
	require.Len(t, attachmentsOverHTTP(t, h, ticketID), want,
		"a refused upload must not be recorded")
}
