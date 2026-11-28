package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// #494: a BASE_URL that cannot be a relying-party id must not stop startup.
func TestConfigurePasskeys(t *testing.T) {
	cases := []struct {
		name, baseURL string
		wantAvailable bool
	}{
		{"a domain", "https://help.example.com", true},
		{"localhost", "http://localhost:8080", true},
		{"an IPv4 address", "http://192.168.1.10:8080", false},
		{"an IPv6 address", "http://[::1]:8080", false},
		{"a single-label host", "http://helpdesk:8080", false},
		{"a non-ASCII host", "https://hélp.example.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			log := slog.New(slog.NewTextHandler(&logs, nil))

			svc, reason := configurePasskeys(tc.baseURL, "Help Desk", log)

			if tc.wantAvailable {
				require.NotNil(t, svc)
				require.Empty(t, reason)
				require.Empty(t, logs.String(), "a working configuration should not warn")
				return
			}
			require.Nil(t, svc)
			require.NotEmpty(t, reason, "the admin banner needs a reason")
			require.Equal(t, 1, strings.Count(logs.String(), "passkeys unavailable"),
				"startup should say so exactly once; got %q", logs.String())
		})
	}
}
