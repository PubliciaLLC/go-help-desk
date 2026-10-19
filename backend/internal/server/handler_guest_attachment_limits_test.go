package server_test

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// #445, Erik's ruling: a per-ticket cap and a per-link hourly rate on guest
// uploads, both admin settings, both small by default.
const (
	perTicketKey = "guest_attachment_max_per_ticket"
	perHourKey   = "guest_attachment_uploads_per_hour"
)

func setInt(t *testing.T, h *harness, key string, v int) {
	t.Helper()
	require.NoError(t, h.adminSvc.SetInt(context.Background(), key, v))
}

// guestUploadsOn is the common setup: uploads on, and the limit under test
// the only one in the way.
func guestUploadsOn(t *testing.T, h *harness, perTicket, perHour int) (ticket.Ticket, string) {
	t.Helper()
	tk, token := seedGuestTicket(t, h)
	setGuestUploads(t, h, true)
	setInt(t, h, perTicketKey, perTicket)
	setInt(t, h, perHourKey, perHour)
	return tk, token
}

func TestGuestUploadLimits_DefaultsApplyOnUpgrade(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.Equal(t, 5, h.adminSvc.GuestAttachmentMaxPerTicket(ctx))
	require.Equal(t, 3, h.adminSvc.GuestAttachmentUploadsPerHour(ctx))
	for _, bad := range []int{0, -1, 51, 1000} {
		setInt(t, h, perTicketKey, bad)
		require.Equal(t, 5, h.adminSvc.GuestAttachmentMaxPerTicket(ctx), "stored %d", bad)
	}
	for _, bad := range []int{0, -1, 31, 1000} {
		setInt(t, h, perHourKey, bad)
		require.Equal(t, 3, h.adminSvc.GuestAttachmentUploadsPerHour(ctx), "stored %d", bad)
	}
}

func TestGuestUploadLimits_PerTicketCapRefusesTheNextOne(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk, token := guestUploadsOn(t, h, 2, 30)

	for i := range 2 {
		res := uploadGuestAttachment(t, h, token, "f"+strconv.Itoa(i)+".txt", []byte("x"))
		require.Equal(t, http.StatusCreated, res.StatusCode, "upload %d of the 2 allowed: %s", i+1, readBody(t, res))
	}
	res := uploadGuestAttachment(t, h, token, "third.txt", []byte("x"))
	body := readBody(t, res)
	require.Equal(t, http.StatusConflict, res.StatusCode, body)
	require.Equal(t, "guest_attachment_limit", errCode(t, []byte(body)))
	require.Contains(t, body, "This ticket already has the most files a guest can attach")

	atts, err := h.ticketSvc.ListAttachments(context.Background(), tk.ID)
	require.NoError(t, err)
	require.Len(t, atts, 2)
}

func TestGuestUploadLimits_StaffAttachmentsDoNotCountTowardTheCap(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk, token := guestUploadsOn(t, h, 2, 30)

	for i := range 3 {
		res := uploadAttachmentAs(t, h, h.apiKey, tk.ID.String(), "staff"+strconv.Itoa(i)+".txt", []byte("s"))
		require.Equal(t, http.StatusCreated, res.StatusCode, readBody(t, res))
	}
	for i := range 2 {
		res := uploadGuestAttachment(t, h, token, "g"+strconv.Itoa(i)+".txt", []byte("x"))
		require.Equal(t, http.StatusCreated, res.StatusCode, "guest %d after three staff files: %s", i+1, readBody(t, res))
	}
	res := uploadGuestAttachment(t, h, token, "g3.txt", []byte("x"))
	require.Equal(t, http.StatusConflict, res.StatusCode, readBody(t, res))
}

func TestGuestUploadLimits_RateIsPerLinkPerHourAndRecovers(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	now := time.Now()
	h.srv.SetGuestUploadClockForTest(func() time.Time { return now })
	_, token := guestUploadsOn(t, h, 50, 2)
	_, other := seedGuestTicketFor(t, h, "other@test.local")

	for i := range 2 {
		res := uploadGuestAttachment(t, h, token, "f"+strconv.Itoa(i)+".txt", []byte("x"))
		require.Equal(t, http.StatusCreated, res.StatusCode, readBody(t, res))
	}
	res := uploadGuestAttachment(t, h, token, "third.txt", []byte("x"))
	body := readBody(t, res)
	require.Equal(t, http.StatusTooManyRequests, res.StatusCode, body)
	require.Equal(t, "rate_limited", errCode(t, []byte(body)))
	secs, err := strconv.Atoi(res.Header.Get("Retry-After"))
	require.NoError(t, err, "Retry-After is whole seconds")
	require.InDelta(t, 3600, secs, 5, "the window is an hour")

	// Keyed by the ticket, not the address: every request here comes from the
	// same client, and another guest's link is not held up by this one's.
	ok := uploadGuestAttachment(t, h, other, "o.txt", []byte("x"))
	require.Equal(t, http.StatusCreated, ok.StatusCode, readBody(t, ok))

	now = now.Add(time.Hour + time.Second)
	again := uploadGuestAttachment(t, h, token, "later.txt", []byte("x"))
	require.Equal(t, http.StatusCreated, again.StatusCode, "after the window: "+readBody(t, again))
}

func TestGuestUploadLimits_ARefusedByCapUploadDoesNotSpendTheRate(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	_, token := guestUploadsOn(t, h, 1, 2)
	require.Equal(t, http.StatusCreated, uploadGuestAttachment(t, h, token, "a.txt", []byte("x")).StatusCode)
	for range 5 {
		res := uploadGuestAttachment(t, h, token, "b.txt", []byte("x"))
		require.Equal(t, http.StatusConflict, res.StatusCode, "the cap answers, never the rate: "+readBody(t, res))
	}
}

func TestGuestUploadLimits_SettingsValidateTheirRanges(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := signInAsAdmin(t, h)
	patch := func(key string, v any) (int, string) {
		res, b := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{key: v})
		return res.StatusCode, string(b)
	}
	for _, c := range []struct {
		key      string
		lo, hi   int
		read     func() int
		fallback int
	}{
		{perTicketKey, 1, 50, func() int { return h.adminSvc.GuestAttachmentMaxPerTicket(context.Background()) }, 5},
		{perHourKey, 1, 30, func() int { return h.adminSvc.GuestAttachmentUploadsPerHour(context.Background()) }, 3},
	} {
		for _, v := range []int{c.lo, c.hi, c.lo + 1} {
			code, b := patch(c.key, v)
			require.Equal(t, http.StatusNoContent, code, "%s=%d: %s", c.key, v, b)
			require.Equal(t, v, c.read())
		}
		for name, v := range map[string]any{"zero": 0, "below": c.lo - 1, "above": c.hi + 1, "string": "3", "null": nil, "fraction": 1.5} {
			code, b := patch(c.key, v)
			require.Equal(t, http.StatusBadRequest, code, "%s %s: %s", c.key, name, b)
			require.Equal(t, c.lo+1, c.read(), "%s %s must not be stored", c.key, name)
		}
	}
}

func TestGuestUploadLimits_AreSessionOnly(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	for _, key := range []string{perTicketKey, perHourKey} {
		res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{key: 4})
		b := readAll(t, res)
		require.Equal(t, http.StatusForbidden, res.StatusCode, key)
		require.Equal(t, "session_required", errCode(t, b), key)
		require.True(t, slices.Contains(admin.AuthCriticalKeys(), key), key)
		require.True(t, slices.Contains(admin.KnownKeys(), key), key)
	}
}

func TestSiteConfig_CarriesThePerTicketCap(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	_, _ = guestUploadsOn(t, h, 7, 3)
	cfg := siteConfig(t, h)
	require.EqualValues(t, 7, cfg["guest_attachment_max_per_ticket"])
}
