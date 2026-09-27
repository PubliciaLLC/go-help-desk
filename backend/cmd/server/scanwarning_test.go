package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// An instance that is not scanning attachments says so at startup.
//
// Not a refusal to boot: an unset CLAMAV_ADDR is a supported configuration.
// It is loud because the alternative is silence, and silence is what makes it
// dangerous. Docker Compose used to hardcode the scanner's address, so an
// operator who upgrades past the change that made it opt-in (#297) loses
// scanning on an instance where it had been running, because of a file they
// never edited. The admin panel reports it, but only to somebody who goes and
// looks.
//
// This exercises the logging shape rather than main() itself, which loads a
// real config and opens a database.
func TestScanningOffWarning(t *testing.T) {
	warn := func(addr string) map[string]any {
		var buf bytes.Buffer
		log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
		emitScanWarning(log, addr)
		if buf.Len() == 0 {
			return nil
		}
		var out map[string]any
		require.NoError(t, json.Unmarshal(buf.Bytes(), &out))
		return out
	}

	t.Run("unset: says what is true, what it costs, and what to do", func(t *testing.T) {
		got := warn("")
		require.NotNil(t, got, "an instance that is not scanning said nothing at all")
		require.Equal(t, "WARN", got["level"])
		require.Contains(t, got["msg"], "OFF",
			"the message does not make it obvious scanning is off: %v", got["msg"])
		require.Contains(t, got["impact"], "without being checked")
		require.Contains(t, got["fix"], "CLAMAV_ADDR")
	})

	t.Run("set: no warning", func(t *testing.T) {
		got := warn("tcp://clamav:3310")
		if got != nil {
			require.NotEqual(t, "WARN", got["level"],
				"an instance that IS scanning was warned that it is not")
		}
	})

	// The architecture note rides along, so somebody turning scanning on
	// learns why it is slow here rather than from a failed pull.
	if runtime.GOARCH == "arm64" {
		t.Run("on arm64, the emulation cost is named", func(t *testing.T) {
			off := warn("")
			require.Contains(t, off["note"], "linux/amd64",
				"the arm64 note is missing from the off warning")

			on := warn("tcp://clamav:3310")
			require.NotNil(t, on, "nothing was said about emulation while scanning is on")
			require.Equal(t, "INFO", on["level"], "this is a note, not a warning")
			require.True(t,
				strings.Contains(on["why"].(string), "amd64") ||
					strings.Contains(on["msg"].(string), "emulation"),
				"the note does not explain why: %v", on)
		})
	}
}
