package user_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Email addresses must be ASCII, so that they compare reliably.
func TestValidateEmail_RefusesNonASCII(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		ok   bool
	}{
		{"ASCII", "Jim@Example.com", true},
		{"accented letter in the local part", "jüm@corp.test", false},
		{"accented letter, second example", "josé@example.com", false},
		{"non-ASCII letter in the domain", "jim@exämple.com", false},
		{"Cyrillic letter", "jim@exаmple.com", false},
		{"fullwidth letters", "ｊｉｍ@example.com", false},
		{"non-ASCII capital letter", "jİm@example.com", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := user.ValidateEmail(tc.in)
			if tc.ok {
				require.NoError(t, err)
				require.Equal(t, "jim@example.com", got)
				return
			}
			require.ErrorIs(t, err, user.ErrValidation)
		})
	}
}

func TestIsEmailDomainAllowed_NonASCIIDomainDoesNotMatch(t *testing.T) {
	require.False(t, user.IsEmailDomainAllowed("jim@exämple.test", []string{"example.test"}))
	require.True(t, user.IsEmailDomainAllowed("jim@example.test", []string{"example.test"}))
}
