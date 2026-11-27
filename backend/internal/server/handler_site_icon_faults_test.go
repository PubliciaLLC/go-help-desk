package server_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/siteicon"
	"github.com/publiciallc/go-help-desk/backend/internal/server"
)

// fileFaultStore reads the metadata but cannot read a file.
type fileFaultStore struct{ siteicon.Store }

func (fileFaultStore) File(context.Context, string) ([]byte, string, bool, error) {
	return nil, "", false, errors.New("injected file read failure")
}

// The public icon route is the one place a storage fault is not hidden behind a
// fallback (the page and the manifest fall back to the built-in icon): a file
// that cannot be read is a 500 storage_error, in the usual error envelope, with
// nothing of the fault in the message, and not a 404 that a browser would cache
// as "there is no such icon".
func TestSiteIconRoute_StorageFaultIs500(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	logs := captureLogs(t)

	server.WithSiteIcons(fileFaultStore{})(h.srv)

	for _, name := range []string{"favicon.ico", "icon-192.png"} {
		res := h.publicGet("/api/v1/branding/icons/" + name)
		status, code, msg := readAPIError(t, res)
		require.Equal(t, http.StatusInternalServerError, status, name)
		require.Equal(t, "storage_error", code)
		require.NotContains(t, msg, "injected", "the fault is logged, not sent")
		require.Contains(t, logs.String(), "injected file read failure")
		require.Contains(t, res.Header.Get("Content-Type"), "application/json")
		require.NotEmpty(t, res.Header.Get("Content-Security-Policy"), "the security headers are on the refusal too")
		require.Empty(t, res.Header.Get("ETag"), "a failure is not a cacheable icon")
	}

	// A name that is not served is a 404 whatever the store is doing.
	res := h.publicGet("/api/v1/branding/icons/master.png")
	res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode)
}
