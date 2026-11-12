package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

// slaToggle is the reader handed to ticket.Service.SetSLAEnabled, which gates
// the SLA re-match a priority change makes (#443b). It reads the same
// admin-settings row gatedSLA reads, live on every call, and a failed read
// counts as off, logged at this boundary because domain code does not log.
func TestSLAToggle_ReadsTheLiveSettingAndCountsAnErrorAsOff(t *testing.T) {
	admin := &mutableEnabler{on: false}
	var buf bytes.Buffer
	read := slaToggle(admin, slog.New(slog.NewTextHandler(&buf, nil)))

	require.False(t, read(context.Background()), "off reads as off")

	admin.on = true
	require.True(t, read(context.Background()), "the very next call picks the toggle up, with no restart")

	admin.on = false
	require.False(t, read(context.Background()), "and turning it off takes effect just as live")
	require.Empty(t, buf.String(), "a successful read logs nothing")

	admin.on = true
	admin.err = errors.New("connection reset by peer")
	require.False(t, read(context.Background()), "a read failure counts as off, even when the setting is on")
	require.Contains(t, buf.String(), "reading SLA enabled setting failed")
	require.Contains(t, buf.String(), "connection reset by peer")
}
