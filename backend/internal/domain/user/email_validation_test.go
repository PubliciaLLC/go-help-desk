package user_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Nothing in this application validated an email address. Register stored
// whatever arrived, Validate only checked non-empty, and the sole
// mail.ParseAddress lived in the mail sender — so an address containing CRLF
// was accepted at signup, written to the database, and became a real account.
func TestValidateEmail(t *testing.T) {
	t.Run("rejected", func(t *testing.T) {
		for _, tc := range []struct{ name, in string }{
			{"CRLF header injection", "attacker@evil.test\r\nBcc: victim@example.com"},
			{"bare LF", "a@b.test\nX-Injected: yes"},
			{"bare CR", "a@b.test\rX-Injected: yes"},
			{"not an address", "not an email at all"},
			{"empty", ""},
			{"whitespace only", "   "},
			{"no domain", "nobody@"},
			{"no local part", "@example.com"},
			// Parses fine and stores a different address than it appears to.
			{"display name attached", "Attacker <victim@example.com>"},
			{"two addresses", "a@b.test, c@d.test"},
			{"angle brackets with name", `"Ops" <ops@example.com>`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, err := user.ValidateEmail(tc.in)
				require.Error(t, err, "%q must be refused", tc.in)
			})
		}
	})

	t.Run("accepted and normalised", func(t *testing.T) {
		for _, tc := range []struct{ in, want string }{
			{"user@example.com", "user@example.com"},
			{"User@Example.COM", "user@example.com"},
			{"  user@example.com  ", "user@example.com"},
			{"first.last+tag@sub.example.co.uk", "first.last+tag@sub.example.co.uk"},
		} {
			got, err := user.ValidateEmail(tc.in)
			require.NoError(t, err, "%q must be accepted", tc.in)
			require.Equal(t, tc.want, got)
		}
	})
}

// Validate is the gate every creation path goes through — admin-created users
// and SSO just-in-time provisioning included, not only signup.
func TestUserValidate_RejectsAMalformedEmail(t *testing.T) {
	u := user.User{
		Email:       "attacker@evil.test\r\nBcc: victim@example.com",
		DisplayName: "Attacker",
		Role:        user.RoleUser,
	}
	require.Error(t, u.Validate(), "a malformed address must not reach the database")

	u.Email = "fine@example.com"
	require.NoError(t, u.Validate())
}
