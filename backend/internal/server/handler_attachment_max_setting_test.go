package server_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// #673: PATCH /admin/settings validates attachment_max_mb (1 to 100) and holds
// guest_attachment_max_mb to the overall limit as it will be after the PATCH.

const attachmentMaxMBKey = "attachment_max_mb" // the wire name, spelled out on purpose

// stored reads the raw row, so "not stored" is distinguishable from "stored as
// the default". ok is false when the row does not exist.
func stored(t *testing.T, h *harness, key string) (v int, ok bool) {
	t.Helper()
	v, err := h.adminSvc.GetInt(context.Background(), key)
	return v, err == nil
}

func patchAttachmentSettings(t *testing.T, sess *session, body map[string]any) (int, string) {
	t.Helper()
	res, b := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", body)
	return res.StatusCode, string(b)
}

func TestAttachmentMaxMBSetting_AcceptsOneToOneHundred(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := signInAsAdmin(t, h)

	for _, mb := range []int{1, 25, 100} {
		code, b := patchAttachmentSettings(t, sess, map[string]any{attachmentMaxMBKey: mb})
		require.Equal(t, http.StatusNoContent, code, "%d: %s", mb, b)
		got, ok := stored(t, h, attachmentMaxMBKey)
		require.True(t, ok)
		require.Equal(t, mb, got)
	}
}

func TestAttachmentMaxMBSetting_RefusesWhatIsNotAnIntegerInRange(t *testing.T) {
	for name, v := range map[string]any{"zero": 0, "101": 101, "string": "25", "null": nil, "fraction": 1.5} {
		t.Run(name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			sess := signInAsAdmin(t, h)

			code, b := patchAttachmentSettings(t, sess, map[string]any{attachmentMaxMBKey: v})
			require.Equal(t, http.StatusBadRequest, code, b)
			require.NotContains(t, b, "unknown_setting")
			_, ok := stored(t, h, attachmentMaxMBKey)
			require.False(t, ok, "must not be stored")
		})
	}

	t.Run("out of range names the bounds", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		_, b := patchAttachmentSettings(t, signInAsAdmin(t, h), map[string]any{attachmentMaxMBKey: 101})
		require.Contains(t, b, "attachment_max_mb must be between 1 and 100")
	})
}

func TestAttachmentMaxMBSetting_IsSessionOnly(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{attachmentMaxMBKey: 50})
	b := readAll(t, res)
	require.Equal(t, http.StatusForbidden, res.StatusCode, string(b))
	require.Equal(t, "session_required", errCode(t, b))
	_, ok := stored(t, h, attachmentMaxMBKey)
	require.False(t, ok)
}

func TestGuestMaxMBSetting_CannotExceedTheOverallLimit(t *testing.T) {
	cases := []struct {
		name        string
		preOverall  int // 0 leaves the overall limit unset
		body        map[string]any
		want        int
		wantMsg     string
		wantOverall int // 0 means not stored
		wantGuest   int // 0 means not stored
	}{
		{name: "default install refuses 26", body: map[string]any{guestMaxMBKey: 26},
			want: http.StatusBadRequest, wantMsg: "between 1 and 25 MB, the per-file limit for every upload"},
		{name: "default install accepts 25", body: map[string]any{guestMaxMBKey: 25},
			want: http.StatusNoContent, wantGuest: 25},
		{name: "raised together in one request", body: map[string]any{attachmentMaxMBKey: 50, guestMaxMBKey: 40},
			want: http.StatusNoContent, wantOverall: 50, wantGuest: 40},
		{name: "lowered below the guest in one request is refused whole", body: map[string]any{attachmentMaxMBKey: 10, guestMaxMBKey: 20},
			want: http.StatusBadRequest, wantMsg: "between 1 and 10 MB"},
		{name: "ceiling is the stored overall limit when the body has none", preOverall: 10, body: map[string]any{guestMaxMBKey: 11},
			want: http.StatusBadRequest, wantMsg: "between 1 and 10 MB", wantOverall: 10},
		{name: "a bad overall value is the error named", body: map[string]any{attachmentMaxMBKey: 101, guestMaxMBKey: 200},
			want: http.StatusBadRequest, wantMsg: "attachment_max_mb must be between 1 and 100"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			sess := signInAsAdmin(t, h)
			if tc.preOverall != 0 {
				setAttachmentMaxMB(t, h, tc.preOverall)
			}

			code, b := patchAttachmentSettings(t, sess, tc.body)
			require.Equal(t, tc.want, code, b)
			if tc.wantMsg != "" {
				require.Equal(t, "bad_request", errCode(t, []byte(b)))
				require.Contains(t, b, tc.wantMsg)
			}
			gotO, okO := stored(t, h, attachmentMaxMBKey)
			require.Equal(t, tc.wantOverall != 0, okO, "overall stored?")
			if okO {
				require.Equal(t, tc.wantOverall, gotO)
			}
			gotG, okG := stored(t, h, guestMaxMBKey)
			require.Equal(t, tc.wantGuest != 0, okG, "guest stored?")
			if okG {
				require.Equal(t, tc.wantGuest, gotG)
			}
		})
	}
}

// Lowering the overall limit under a stored guest figure is accepted and leaves
// the guest figure alone; the guest is held to the lower value until either
// changes, and raising the overall limit again restores the guest's own figure.
func TestGuestMaxMBSetting_LoweringTheOverallLimitKeepsTheGuestFigure(t *testing.T) {
	const mib = 1 << 20
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := signInAsAdmin(t, h)
	_, token := seedGuestTicket(t, h)
	setGuestUploads(t, h, true)

	code, b := patchAttachmentSettings(t, sess, map[string]any{guestMaxMBKey: 20})
	require.Equal(t, http.StatusNoContent, code, b)

	code, b = patchAttachmentSettings(t, sess, map[string]any{attachmentMaxMBKey: 10})
	require.Equal(t, http.StatusNoContent, code, b)
	require.Equal(t, 20, h.adminSvc.GuestAttachmentMaxMB(context.Background()), "the guest row is not rewritten")

	over := uploadGuestAttachment(t, h, token, "big.txt", bytes.Repeat([]byte("a"), 10*mib+1))
	body := readBody(t, over)
	require.Equal(t, http.StatusRequestEntityTooLarge, over.StatusCode, body)
	require.Contains(t, body, "10 MB")

	code, b = patchAttachmentSettings(t, sess, map[string]any{attachmentMaxMBKey: 25})
	require.Equal(t, http.StatusNoContent, code, b)
	ok := uploadGuestAttachment(t, h, token, "mid.txt", bytes.Repeat([]byte("a"), 15*mib))
	require.Equal(t, http.StatusCreated, ok.StatusCode, readBody(t, ok))
}
