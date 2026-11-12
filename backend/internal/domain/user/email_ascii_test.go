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

// An account whose address predates the ASCII rule (or came from an IdP) can
// have a non-ASCII domain, and OIDC checks the address the IdP sends on every
// sign-in. The list holds the xn-- form, so the address is compared by its
// ASCII form (#620). Matching only ever widens, and only for a domain whose
// ASCII form is on the list.
func TestIsEmailDomainAllowed_ANonASCIIDomainMatchesItsPunycodeForm(t *testing.T) {
	allowed := []string{"xn--bcher-kva.de"}
	for _, tc := range []struct {
		name  string
		email string
		want  bool
	}{
		{"U-label domain", "a@bücher.de", true},
		{"upper case", "A@BÜCHER.DE", true},
		{"decomposed u with a combining diaeresis", "a@bücher.de", true},
		{"already in xn-- form", "a@xn--bcher-kva.de", true},
		// ValidateEmail cannot unwrap this one (it refuses non-ASCII), so it is
		// judged by the raw text after the at sign, as it always was.
		{"bracketed", "<a@bücher.de>", false},
		{"another IDN domain", "a@bäcker.de", false},
		{"the ASCII spelling of the same words", "a@bucher.de", false},
		{"a subdomain of it", "a@x.bücher.de", false},
		{"a suffix of it", "a@evilbücher.de", false},
		// A label that UTS-46 maps to plain ASCII (fullwidth letters) is still
		// not a spelling of the listed domain: those addresses were refused
		// before and stay refused.
		{"fullwidth letters mapping to ASCII", "a@ｂｕｃｈｅｒ.de", false},
		{"not convertible (leading hyphen)", "a@-bücher.de", false},
		{"not convertible (empty label)", "a@bü..cher.de", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, user.IsEmailDomainAllowed(tc.email, allowed))
		})
	}
}

// ASCII domains are compared exactly as before.
func TestIsEmailDomainAllowed_ASCIIDomainsAreUnchangedByIDNA(t *testing.T) {
	require.True(t, user.IsEmailDomainAllowed("a@Example.COM", []string{"example.com"}))
	require.False(t, user.IsEmailDomainAllowed("a@example.com", []string{"xn--bcher-kva.de"}))
}
