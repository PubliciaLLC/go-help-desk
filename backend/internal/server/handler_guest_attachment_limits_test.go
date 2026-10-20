package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"sync/atomic"
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

func TestSiteConfig_CarriesTheHourlyFigureWhileUploadsAreOn(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	_, _ = guestUploadsOn(t, h, 7, 4)
	require.EqualValues(t, 4, siteConfig(t, h)["guest_attachment_uploads_per_hour"])
	setGuestUploads(t, h, false)
	require.NotContains(t, siteConfig(t, h), "guest_attachment_uploads_per_hour")
}

// A refusal decided BEFORE the scanner (wrong type, over the size, a request the
// parser refuses) does not cost the guest their hour. One decided after the scan
// does: the scan is the expensive part, and refunding it would let a guest make
// the server scan without limit. The budget is 1 so a spent unit shows at once.
func TestGuestUploadLimits_APreScanMistakeIsRefunded(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	_, token := guestUploadsOn(t, h, 50, 1)
	setGuestMaxMB(t, h, 1)

	cases := []struct {
		name    string
		file    string
		content []byte
		status  int
		code    string
	}{
		{"wrong type", "evil.exe", []byte("x"), http.StatusUnsupportedMediaType, "unsupported_type"},
		{"over the size", "big.txt", bytes.Repeat([]byte("a"), 1<<20+1), http.StatusRequestEntityTooLarge, "too_large"},
		{"a request the form parser refuses", "a\x01b.txt", []byte("x"), http.StatusBadRequest, "bad_request"},
	}
	for _, c := range cases {
		for range 3 { // three refusals in a row against a budget of one
			res := uploadGuestAttachment(t, h, token, c.file, c.content)
			body := readBody(t, res)
			require.Equal(t, c.status, res.StatusCode, "%s: %s", c.name, body)
			require.Equal(t, c.code, errCode(t, []byte(body)), c.name)
		}
	}
	ok := uploadGuestAttachment(t, h, token, "fine.txt", []byte("x"))
	require.Equal(t, http.StatusCreated, ok.StatusCode, "the hour was never spent: "+readBody(t, ok))
	again := uploadGuestAttachment(t, h, token, "second.txt", []byte("x"))
	require.Equal(t, http.StatusTooManyRequests, again.StatusCode, "the accepted one does spend it")
}

// The probe that found the hole: refunded refusals that come after the scan let
// a guest make the scanner work for nothing. Twenty bad PNGs at a budget of one
// are one scan and nineteen 429s.
func TestGuestUploadLimits_PostScanRefusalsCannotMakeTheServerScanWithoutLimit(t *testing.T) {
	addr, scans := countingCleanScanner(t)
	h, cleanup := newHarnessWith(t, 0, addr)
	defer cleanup()
	_, token := guestUploadsOn(t, h, 50, 1)

	var statuses []int
	for range 20 {
		res := uploadGuestAttachment(t, h, token, "bad.png", []byte("this is not a png"))
		statuses = append(statuses, res.StatusCode)
		res.Body.Close()
	}
	require.Equal(t, http.StatusUnprocessableEntity, statuses[0], "the first is decided after the scan")
	for i, st := range statuses[1:] {
		require.Equal(t, http.StatusTooManyRequests, st, "attempt %d", i+2)
	}
	require.EqualValues(t, 1, scans.Load(), "the scanner was asked once, not twenty times")
}

// Refusals before the scan are free of the hour but not free: a second, fixed
// budget per ticket counts every refused attempt, so they cannot stream bodies
// forever either.
func TestGuestUploadLimits_RefusedAttemptsHaveTheirOwnBudget(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	now := time.Now()
	h.srv.SetGuestUploadClockForTest(func() time.Time { return now })
	_, token := guestUploadsOn(t, h, 50, 30)
	_, other := seedGuestTicketFor(t, h, "other-refusals@test.local")

	for i := range 20 {
		res := uploadGuestAttachment(t, h, token, "evil.exe", []byte("x"))
		require.Equal(t, http.StatusUnsupportedMediaType, res.StatusCode, "refusal %d: %s", i+1, readBody(t, res))
	}
	over := uploadGuestAttachment(t, h, token, "evil.exe", []byte("x"))
	body := readBody(t, over)
	require.Equal(t, http.StatusTooManyRequests, over.StatusCode, body)
	require.Equal(t, "rate_limited", errCode(t, []byte(body)))
	require.NotEmpty(t, over.Header.Get("Retry-After"))
	good := uploadGuestAttachment(t, h, token, "fine.txt", []byte("x"))
	require.Equal(t, http.StatusTooManyRequests, good.StatusCode, "the same budget stops a good file too, until the hour passes")

	ok := uploadGuestAttachment(t, h, other, "fine.txt", []byte("x"))
	require.Equal(t, http.StatusCreated, ok.StatusCode, "another link is not held up: "+readBody(t, ok))

	now = now.Add(time.Hour + time.Second)
	later := uploadGuestAttachment(t, h, token, "fine.txt", []byte("x"))
	require.Equal(t, http.StatusCreated, later.StatusCode, "recovers after the window: "+readBody(t, later))
}

// countingCleanScanner is a clamd that says OK and counts the files it was
// asked to scan (PINGs are not scans).
func countingCleanScanner(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	var scans atomic.Int32
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				r := bufio.NewReader(conn)
				cmd, err := r.ReadString(0)
				if err != nil {
					return
				}
				if cmd == "zPING\x00" {
					_, _ = conn.Write([]byte("PONG\x00"))
					return
				}
				scans.Add(1)
				for {
					var size [4]byte
					if _, err := io.ReadFull(r, size[:]); err != nil {
						return
					}
					n := binary.BigEndian.Uint32(size[:])
					if n == 0 {
						break
					}
					if _, err := io.CopyN(io.Discard, r, int64(n)); err != nil {
						return
					}
				}
				_, _ = conn.Write([]byte("stream: OK\x00"))
			}()
		}
	}()
	return "tcp://" + ln.Addr().String(), &scans
}

func TestGuestUploadLimits_AMalwareVerdictIsNotRefunded(t *testing.T) {
	h, cleanup := newHarnessWith(t, 0, fakeInfectedScanner(t))
	defer cleanup()
	_, token := guestUploadsOn(t, h, 50, 1)

	first := uploadGuestAttachment(t, h, token, "nasty.txt", []byte("pretend this is nasty"))
	require.Equal(t, http.StatusUnprocessableEntity, first.StatusCode, readBody(t, first))
	second := uploadGuestAttachment(t, h, token, "nasty2.txt", []byte("again"))
	require.Equal(t, http.StatusTooManyRequests, second.StatusCode, "an infected upload spends the hour: "+readBody(t, second))
}

func TestGuestUploadLimits_AServerFaultIsNotRefunded(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	_, token := guestUploadsOn(t, h, 50, 1)

	breakStorage(t, h)
	down := uploadGuestAttachment(t, h, token, "a.txt", []byte("x"))
	require.Equal(t, http.StatusServiceUnavailable, down.StatusCode, readBody(t, down))
	fixStorage(t, h)
	next := uploadGuestAttachment(t, h, token, "b.txt", []byte("x"))
	require.Equal(t, http.StatusTooManyRequests, next.StatusCode, readBody(t, next))
}

// A reporter with an account is not a guest: their file never counts toward the
// guest cap.
func TestGuestUploadLimits_AReportersFileIsNotAGuestFile(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{DescriptionFormat: ticket.TextFormatPlain,
		Subject: "Mine", Description: "x", CategoryID: h.catID, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)

	res := uploadAttachmentAs(t, h, h.userKey, tk.ID.String(), "mine.txt", []byte("x"))
	require.Equal(t, http.StatusCreated, res.StatusCode, readBody(t, res))
	atts, err := h.ticketSvc.ListAttachments(context.Background(), tk.ID)
	require.NoError(t, err)
	require.Len(t, atts, 1)

	n, err := h.ticketSvc.CountGuestAttachments(context.Background(), tk.ID)
	require.NoError(t, err)
	require.Zero(t, n)
}
