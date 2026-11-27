package server

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A slot of imageWork is given back whatever the work does: four leaked slots
// would stop every image upload on the instance until a restart.
func TestWithImageSlot_ReleasesOnPanic(t *testing.T) {
	base := len(imageWork) // other tests may be holding slots of their own

	ran := false
	withImageSlot(func() {
		ran = true
		require.Greater(t, len(imageWork), base, "the slot is held while the work runs")
	})
	require.True(t, ran)
	require.LessOrEqual(t, len(imageWork), base)

	require.Panics(t, func() { withImageSlot(func() { panic("boom") }) })
	require.LessOrEqual(t, len(imageWork), base, "a panic must not leak the slot")
}
