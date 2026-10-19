package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// #445: a guest may attach a file from the web page only when an administrator
// has said so. The wire names are spelled out here on purpose: a renamed
// constant must not silently rename what administrators and the OpenAPI spec
// see.
const (
	guestUploadsKey  = "guest_attachments_enabled"
	guestMaxMBKey    = "guest_attachment_max_mb"
	globalCapMB      = 25 // attachMaxBytes in handler_attachments.go
	guestRefusalBody = `{"error":{"code":"not_found","message":"not found"}}` + "\n"
)

func setGuestUploads(t *testing.T, h *harness, on bool) {
	t.Helper()
	require.NoError(t, h.adminSvc.SetBool(context.Background(), guestUploadsKey, on))
}

func setGuestMaxMB(t *testing.T, h *harness, mb int) {
	t.Helper()
	require.NoError(t, h.adminSvc.SetInt(context.Background(), guestMaxMBKey, mb))
}

func TestGuestAttachmentsSetting_OffByDefaultRefusesWithTheSame404AsABadLink(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk, token := seedGuestTicket(t, h) // guest submission is on; the new setting is untouched

	res := uploadGuestAttachment(t, h, token, "photo.txt", []byte("x"))
	body := readBody(t, res)
	require.Equal(t, http.StatusNotFound, res.StatusCode, body)
	require.Equal(t, guestRefusalBody, body)

	bad := uploadGuestAttachment(t, h, nearMiss(token), "photo.txt", []byte("x"))
	require.Equal(t, body, readBody(t, bad), "a refusal for the setting must not differ from a dead link's")

	atts, err := h.ticketSvc.ListAttachments(context.Background(), tk.ID)
	require.NoError(t, err)
	require.Empty(t, atts)
}

func TestGuestAttachmentsSetting_OnAccepts(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk, token := seedGuestTicket(t, h)
	setGuestUploads(t, h, true)

	res := uploadGuestAttachment(t, h, token, "photo.txt", []byte("x"))
	body := readBody(t, res)
	require.Equal(t, http.StatusCreated, res.StatusCode, body)

	atts, err := h.ticketSvc.ListAttachments(context.Background(), tk.ID)
	require.NoError(t, err)
	require.Len(t, atts, 1)
}

func TestGuestAttachmentsSetting_OnlyWorksWhileGuestSubmissionIsOn(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	_, token := seedGuestTicket(t, h)
	setGuestUploads(t, h, true)
	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyGuestSubmissionEnabled, false))

	res := uploadGuestAttachment(t, h, token, "photo.txt", []byte("x"))
	require.Equal(t, http.StatusNotFound, res.StatusCode)
	require.Equal(t, guestRefusalBody, readBody(t, res))
}

func TestGuestAttachmentsSetting_OverTheGuestLimitIs413(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk, token := seedGuestTicket(t, h)
	setGuestUploads(t, h, true)
	setGuestMaxMB(t, h, 1)

	over := uploadGuestAttachment(t, h, token, "big.txt", bytes.Repeat([]byte("a"), 1<<20+1))
	body := readBody(t, over)
	require.Equal(t, http.StatusRequestEntityTooLarge, over.StatusCode, body)
	require.Equal(t, "too_large", errCode(t, []byte(body)))
	require.Contains(t, body, "1 MB", "the message names the limit that applied")

	atts, err := h.ticketSvc.ListAttachments(context.Background(), tk.ID)
	require.NoError(t, err)
	require.Empty(t, atts)

	exact := uploadGuestAttachment(t, h, token, "edge.txt", bytes.Repeat([]byte("a"), 1<<20))
	require.Equal(t, http.StatusCreated, exact.StatusCode, readBody(t, exact))
}

func TestGuestAttachmentsSetting_TheInstanceMaximumCapsTheGuestLimit(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	_, token := seedGuestTicket(t, h)
	setGuestUploads(t, h, true)
	setGuestMaxMB(t, h, 100)

	res := uploadGuestAttachment(t, h, token, "big.txt", bytes.Repeat([]byte("a"), globalCapMB<<20+1))
	body := readBody(t, res)
	require.Equal(t, http.StatusRequestEntityTooLarge, res.StatusCode, body)
	require.Contains(t, body, "25 MB")

	ok := uploadGuestAttachment(t, h, token, "fits.txt", bytes.Repeat([]byte("a"), 2<<20))
	require.Equal(t, http.StatusCreated, ok.StatusCode, readBody(t, ok))
}

func TestGuestAttachmentsSetting_Defaults(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.False(t, h.adminSvc.GuestAttachmentsEnabled(ctx), "off until an administrator turns it on, upgrade included")
	require.Equal(t, 5, h.adminSvc.GuestAttachmentMaxMB(ctx))

	// A damaged row reads as the default, not as zero or as 1000 MB.
	for _, bad := range []int{0, -3, 101, 100000} {
		setGuestMaxMB(t, h, bad)
		require.Equal(t, 5, h.adminSvc.GuestAttachmentMaxMB(ctx), "stored %d", bad)
	}

	// Uploads on does nothing without guest submission.
	setGuestUploads(t, h, true)
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyGuestSubmissionEnabled, false))
	require.False(t, h.adminSvc.GuestAttachmentsEnabled(ctx))
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyGuestSubmissionEnabled, true))
	require.True(t, h.adminSvc.GuestAttachmentsEnabled(ctx))
}

func TestGuestAttachmentsSetting_ValidatesOneToOneHundredMB(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := signInAsAdmin(t, h)
	patch := func(key string, v any) (int, string) {
		res, b := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{key: v})
		return res.StatusCode, string(b)
	}

	for _, mb := range []int{1, 5, 25, 99, 100} {
		code, b := patch(guestMaxMBKey, mb)
		require.Equal(t, http.StatusNoContent, code, "%d: %s", mb, b)
		require.Equal(t, mb, h.adminSvc.GuestAttachmentMaxMB(context.Background()))
	}
	for name, v := range map[string]any{"zero": 0, "negative": -1, "101": 101, "string": "5", "null": nil, "fraction": 1.5} {
		code, b := patch(guestMaxMBKey, v)
		require.Equal(t, http.StatusBadRequest, code, "%s: %s", name, b)
		require.NotContains(t, b, "unknown_setting", name)
		require.Equal(t, 100, h.adminSvc.GuestAttachmentMaxMB(context.Background()), "%s must not be stored", name)
	}

	code, b := patch(guestUploadsKey, true)
	require.Equal(t, http.StatusNoContent, code, b)
	code, b = patch(guestUploadsKey, "yes")
	require.Equal(t, http.StatusBadRequest, code, b)
}

func TestGuestAttachmentsSetting_AreSessionOnly(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	for _, c := range []struct {
		key string
		v   any
	}{{guestUploadsKey, true}, {guestMaxMBKey, 50}} {
		res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{c.key: c.v})
		b := readAll(t, res)
		require.Equal(t, http.StatusForbidden, res.StatusCode, c.key)
		require.Equal(t, "session_required", errCode(t, b), c.key)
		require.True(t, slices.Contains(admin.AuthCriticalKeys(), c.key), c.key)
		require.True(t, slices.Contains(admin.KnownKeys(), c.key), c.key)
	}
}

func TestSiteConfig_SaysWhetherGuestsMayAttachAndTheEffectiveLimit(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	get := func() map[string]any {
		rec := httptest.NewRecorder()
		h.srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/site", nil))
		b, _ := io.ReadAll(rec.Result().Body)
		var out map[string]any
		require.NoError(t, json.Unmarshal(b, &out), string(b))
		return out
	}
	ctx := context.Background()

	cfg := get()
	require.Equal(t, false, cfg["guest_attachments_enabled"])

	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyGuestSubmissionEnabled, true))
	setGuestUploads(t, h, true)
	setGuestMaxMB(t, h, 7)
	cfg = get()
	require.Equal(t, true, cfg["guest_attachments_enabled"])
	require.EqualValues(t, 7<<20, cfg["guest_attachment_max_bytes"])

	setGuestMaxMB(t, h, 100)
	require.EqualValues(t, globalCapMB<<20, get()["guest_attachment_max_bytes"], "the page must never promise more than the server accepts")

	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyGuestSubmissionEnabled, false))
	cfg = get()
	require.Equal(t, false, cfg["guest_attachments_enabled"], "dependent on guest submission")
	require.NotContains(t, cfg, "guest_attachment_max_bytes", "nothing to say when it is off")
}
