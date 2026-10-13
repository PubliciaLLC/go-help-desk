package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// APP_ENV does nothing. Setting it to any value, "production" included, logs
// the one-line notice; leaving it unset says nothing.
func TestAppEnvNotice(t *testing.T) {
	cases := []struct {
		name    string
		set     bool
		wantMsg bool
	}{
		{"unset", false, false},
		{"set (any value, including production)", true, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg, shouldLog := appEnvNotice(tc.set)
			require.Equal(t, tc.wantMsg, shouldLog)
			if tc.wantMsg {
				require.Contains(t, msg, "APP_ENV is set but has no effect")
			} else {
				require.Empty(t, msg)
			}
		})
	}
}
