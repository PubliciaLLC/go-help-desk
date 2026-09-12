package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// The unit tests in domain/ticket cover the formatting rules. These cover the
// wiring — that the admin setting actually reaches ticket creation. A prefix
// that formats correctly but never leaves the settings table is the failure
// this catches.

func createTicketAs(t *testing.T, h *harness, subject string) string {
	t.Helper()
	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/tickets", map[string]any{
		"subject":     subject,
		"category_id": h.catID.String(),
		"priority":    "low",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created struct {
		TrackingNumber string `json:"tracking_number"`
	}
	decodeJSON(t, resp, &created)
	return created.TrackingNumber
}

func TestTicketPrefix_DefaultsToGHD(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tn := createTicketAs(t, h, "Default prefix")
	require.Regexp(t, `^GHD-\d{4}-\d{6}$`, tn,
		"an instance with no configured prefix mints GHD")
}

// TestTicketPrefix_ConfiguredIsUsed is the case an instance predating the
// rename needs: set it back to OHD and keep one consistent series.
func TestTicketPrefix_ConfiguredIsUsed(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	require.NoError(t, h.adminSvc.SetString(context.Background(), admin.KeyTicketPrefix, "OHD"))

	tn := createTicketAs(t, h, "Configured prefix")
	require.Regexp(t, `^OHD-\d{4}-\d{6}$`, tn)
}

// TestTicketPrefix_InvalidFallsBack pins that a bad setting cannot stop a
// customer opening a ticket. Refusing here would turn an admin's typo into an
// outage on the most important path in the product.
func TestTicketPrefix_InvalidFallsBack(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	for _, bad := range []string{"", "ghd", "GH-D", "ABCDEFGHI"} {
		require.NoError(t, h.adminSvc.SetString(context.Background(), admin.KeyTicketPrefix, bad))

		tn := createTicketAs(t, h, "Invalid prefix "+bad)
		require.Regexp(t, `^GHD-\d{4}-\d{6}$`, tn,
			"prefix %q must fall back to the default rather than fail or mint a malformed number", bad)
	}
}

// TestTicketPrefix_ExistingNumbersAreNotRewritten is the property that makes
// changing the prefix safe at all: tracking numbers already issued are quoted
// in customer email and referenced in replies, so they must survive.
func TestTicketPrefix_ExistingNumbersAreNotRewritten(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyTicketPrefix, "OHD"))
	before := createTicketAs(t, h, "Issued under the old prefix")
	require.Regexp(t, `^OHD-`, before)

	require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyTicketPrefix, "GHD"))
	after := createTicketAs(t, h, "Issued under the new prefix")
	require.Regexp(t, `^GHD-`, after)

	// The old one is unchanged and still reachable by its original number,
	// which is how a customer quoting it in a reply finds their ticket.
	resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets/"+before, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode,
		"a ticket issued under the previous prefix must still resolve")

	var got struct {
		TrackingNumber string `json:"tracking_number"`
	}
	decodeJSON(t, resp, &got)
	require.Equal(t, before, got.TrackingNumber, "existing numbers are never rewritten")
}

// An invalid prefix used to be accepted with 204 and then silently ignored at
// mint time in favour of the default — the admin's setting did nothing and
// nothing said so. ticket.go claimed this was "enforced where the setting is
// saved"; now it is.
func TestUpdateSettings_RejectsInvalidTicketPrefix(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	cases := []struct {
		name   string
		prefix string
	}{
		{"lowercase", "ghd"},
		{"hyphenated", "GH-D"},
		{"too long", "ABCDEFGHI"},
		{"empty", ""},
		{"punctuation", "GHD!"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings",
				map[string]any{admin.KeyTicketPrefix: tc.prefix})
			res.Body.Close()
			require.Equal(t, http.StatusBadRequest, res.StatusCode,
				"an invalid prefix must be refused, not accepted and ignored")
		})
	}

	// And a valid one still saves.
	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{admin.KeyTicketPrefix: "IT2"})
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.Equal(t, "IT2", h.adminSvc.TicketPrefix(context.Background()))
}
