package user_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Go's strings.ToLower and Postgres lower() fold some non-ASCII letters into
// ASCII ones, so an address containing one cannot be compared reliably.
// ValidateEmail refuses it before any lower-casing happens.
func TestValidateEmail_RefusesNonASCII(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		ok   bool
	}{
		{"ASCII", "Jim@Example.com", true},
		{"U+0130 in the domain", "jim@xİ.com", false},
		{"U+0130 in the local part", "jİm@example.com", false},
		{"Kelvin sign in the local part", "Kim@example.com", false},
		{"Kelvin sign in the domain", "jim@Korp.com", false},
		{"Cyrillic a", "jim@exаmple.com", false},
		{"fullwidth letters", "ｊｉｍ@example.com", false},
		{"fullwidth at sign", "jim＠example.com", false},
		{"non-ASCII display name", "Jİm <jim@example.com>", false},
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
	require.False(t, user.IsEmailDomainAllowed("jim@xİ.test", []string{"xi.test"}))
	require.True(t, user.IsEmailDomainAllowed("jim@xi.test", []string{"xi.test"}))
}
