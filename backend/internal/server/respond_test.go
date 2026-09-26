package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/stretchr/testify/require"
)

// TestHandleError_MapsSentinelsToStatus runs entirely without a database
// (package server, not server_test, so it can call the unexported handleError
// directly): it is the single place that pins the status code every mapped
// sentinel gets, so a future addition or reshuffle of the errors.Is chain in
// handleError is caught here instead of only through an integration test.
// Runs in CI's non-DB `go test ./...` step.
func TestHandleError_MapsSentinelsToStatus(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{
			name:       "wrapped ticket.ErrNotClosed maps to 409 (#277)",
			err:        fmt.Errorf("reopening: %w", ticket.ErrNotClosed),
			wantStatus: http.StatusConflict,
		},
		{
			name:       "wrapped ticket.ErrValidation maps to 400",
			err:        fmt.Errorf("no valid reopen target status is configured: %w", ticket.ErrValidation),
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "sla.ErrValidation maps to 400 (#276)",
			err:        sla.ErrValidation,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "sla.ErrUnknownCategory maps to 400 (#276)",
			err:        sla.ErrUnknownCategory,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "an unrecognized error falls through to 500",
			err:        errors.New("boom"),
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handleError(rec, tc.err)
			require.Equal(t, tc.wantStatus, rec.Code)
		})
	}
}
