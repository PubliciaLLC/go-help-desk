package main

import (
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
	"github.com/stretchr/testify/require"
)

func TestAppEnvNotice(t *testing.T) {
	cases := []struct {
		name      string
		appEnv    string
		wantMsg   bool
		msgSearch string
	}{
		{
			name:    "unset (empty string default)",
			appEnv:  "",
			wantMsg: false,
		},
		{
			name:    "production",
			appEnv:  "production",
			wantMsg: false,
		},
		{
			name:      "development",
			appEnv:    "development",
			wantMsg:   true,
			msgSearch: "APP_ENV is set but has no effect",
		},
		{
			name:      "other value",
			appEnv:    "staging",
			wantMsg:   true,
			msgSearch: "APP_ENV is set but has no effect",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{AppEnv: tc.appEnv}
			msg, shouldLog := appEnvNotice(cfg)
			if tc.wantMsg {
				require.True(t, shouldLog, "expected to log but shouldLog was false")
				require.NotEmpty(t, msg, "expected a message but got empty string")
				require.Contains(t, msg, tc.msgSearch, "message does not contain expected text")
			} else {
				require.False(t, shouldLog, "expected not to log but shouldLog was true")
				require.Empty(t, msg, "expected empty message but got: %s", msg)
			}
		})
	}
}
