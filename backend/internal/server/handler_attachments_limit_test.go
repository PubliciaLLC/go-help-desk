package server_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// #673: the per-file limit is the attachment_max_mb setting, 25 when unset.
// Both the declared-size check and the body cap follow it.

func setAttachmentMaxMB(t *testing.T, h *harness, mb int) {
	t.Helper()
	require.NoError(t, h.adminSvc.SetInt(context.Background(), admin.KeyAttachmentMaxMB, mb))
}

func TestUpload_PerFileLimitFollowsTheSetting(t *testing.T) {
	const mib = 1 << 20
	cases := []struct {
		name    string
		setMB   int // 0 leaves the setting unset
		size    int
		want    int
		wantMsg string
	}{
		{name: "unset, 25 MiB fits", size: 25 * mib, want: http.StatusCreated},
		{name: "unset, 25 MiB + 1 is declared too large", size: 25*mib + 1, want: http.StatusRequestEntityTooLarge, wantMsg: "file exceeds 25 MB limit"},
		{name: "10, 10 MiB fits", setMB: 10, size: 10 * mib, want: http.StatusCreated},
		{name: "10, 10 MiB + 1 is declared too large", setMB: 10, size: 10*mib + 1, want: http.StatusRequestEntityTooLarge, wantMsg: "file exceeds 10 MB limit"},
		{name: "10, an oversize body is cut off by the body cap", setMB: 10, size: 12 * mib, want: http.StatusRequestEntityTooLarge, wantMsg: "file exceeds 10 MB limit"},
		{name: "30, 26 MiB fits (more than the old constant)", setMB: 30, size: 26 * mib, want: http.StatusCreated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			if tc.setMB != 0 {
				setAttachmentMaxMB(t, h, tc.setMB)
			}
			id := ticketForUpload(t, h)

			res := uploadAttachment(t, h, id, "big.txt", bytes.Repeat([]byte("a"), tc.size))
			body := readBody(t, res)
			require.Equal(t, tc.want, res.StatusCode, body)
			if tc.wantMsg != "" {
				require.Equal(t, "too_large", errCode(t, []byte(body)))
				require.Contains(t, body, tc.wantMsg)
			}
		})
	}
}

func TestGuestUpload_IsHeldToTheLowerOfGuestAndOverallLimit(t *testing.T) {
	const mib = 1 << 20
	cases := []struct {
		name      string
		overallMB int
		guestMB   int
		wantMB    int
	}{
		{name: "guest above overall", overallMB: 10, guestMB: 20, wantMB: 10},
		{name: "guest below overall", overallMB: 10, guestMB: 5, wantMB: 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			_, token := seedGuestTicket(t, h)
			setGuestUploads(t, h, true)
			setAttachmentMaxMB(t, h, tc.overallMB)
			setGuestMaxMB(t, h, tc.guestMB)

			over := uploadGuestAttachment(t, h, token, "big.txt", bytes.Repeat([]byte("a"), tc.wantMB*mib+1))
			body := readBody(t, over)
			require.Equal(t, http.StatusRequestEntityTooLarge, over.StatusCode, body)
			require.Contains(t, body, fmt.Sprintf("file exceeds %d MB limit", tc.wantMB))

			exact := uploadGuestAttachment(t, h, token, "edge.txt", bytes.Repeat([]byte("a"), tc.wantMB*mib))
			require.Equal(t, http.StatusCreated, exact.StatusCode, readBody(t, exact))

			require.EqualValues(t, tc.wantMB*mib, siteConfig(t, h)["guest_attachment_max_bytes"],
				"the page must promise exactly what the server accepts")
		})
	}
}
