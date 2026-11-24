package admin_test

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// #673: attachment_max_mb is the instance-wide per-file limit. Unset, damaged
// or out of range it reads as 25, so a bad row can neither lift the limit nor
// drop it to zero.
func TestAttachmentMaxMB_Reader(t *testing.T) {
	cases := []struct {
		name string
		raw  string // stored JSON; "" means nothing stored
		want int
	}{
		{name: "unset reads 25", raw: "", want: 25},
		{name: "zero reads 25", raw: "0", want: 25},
		{name: "negative reads 25", raw: "-1", want: 25},
		{name: "101 reads 25", raw: "101", want: 25},
		{name: "a string reads 25", raw: `"50"`, want: 25},
		{name: "one reads as stored", raw: "1", want: 1},
		{name: "fifty reads as stored", raw: "50", want: 50},
		{name: "one hundred reads as stored", raw: "100", want: 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			svc := admin.NewService(newFakeAdminStore())
			if tc.raw != "" {
				require.NoError(t, svc.SetRaw(ctx, admin.KeyAttachmentMaxMB, []byte(tc.raw)))
			}
			require.Equal(t, tc.want, svc.AttachmentMaxMB(ctx))
		})
	}
}

func TestAttachmentMaxMB_Constants(t *testing.T) {
	require.Equal(t, "attachment_max_mb", admin.KeyAttachmentMaxMB)
	require.Equal(t, 25, admin.AttachmentMaxMBDefault)
	require.Equal(t, 1, admin.AttachmentMaxMBMin)
	require.Equal(t, 100, admin.AttachmentMaxMBMax)
}

func TestValidAttachmentMaxMB(t *testing.T) {
	cases := []struct {
		v    int
		want bool
	}{
		{-1, false},
		{0, false},
		{1, true},
		{25, true},
		{100, true},
		{101, false},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, admin.ValidAttachmentMaxMB(tc.v), "%d", tc.v)
	}
}

// Session-only: a leaked API key must not widen what the instance accepts.
func TestAttachmentMaxMB_IsAuthCriticalAndKnown(t *testing.T) {
	require.Contains(t, admin.AuthCriticalKeys(), admin.KeyAttachmentMaxMB)
	require.True(t, slices.Contains(admin.KnownKeys(), admin.KeyAttachmentMaxMB))
}
