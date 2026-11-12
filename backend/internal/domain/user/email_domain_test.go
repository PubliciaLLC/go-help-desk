package user_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// #402: the allow-list judges the address the way the account will be stored,
// which is ValidateEmail's answer, not the raw string the IdP sent. A bracketed
// address used to be read as the domain "x.com>" and refused. The check is a
// gate: whatever it cannot make sense of it refuses.
func TestIsEmailDomainAllowed(t *testing.T) {
	allowed := []string{"x.com"}
	cases := []struct {
		name  string
		email string
		want  bool
	}{
		{"plain", "a@x.com", true},
		{"uppercase domain", "a@X.COM", true},
		{"mixed case everywhere", "A@X.com", true},
		{"padded with spaces", "  a@x.com  ", true},
		{"angle brackets", "<a@x.com>", true},
		{"angle brackets, uppercase domain", "<a@X.COM>", true},
		{"quotes that were never needed", `"a"@x.com`, true},

		{"other domain", "a@y.com", false},
		{"subdomain of an allowed domain", "a@sub.x.com", false},
		{"allowed domain as a suffix", "a@evilx.com", false},
		{"allowed domain as the local part", "x.com@y.com", false},
		{"unclosed bracket", "a@x.com>", false},
		{"no domain", "a@", false},
		{"no at sign", "x.com", false},
		{"empty", "", false},
		{"display name attached", "Eve <a@x.com>", false},
		{"display name naming the allowed domain", "a@x.com <a@y.com>", false},
		{"two addresses", "a@y.com, b@x.com", false},
		{"two addresses, allowed first", "a@x.com, b@y.com", false},
		{"quoted local part naming the allowed domain", `"a@x.com"@y.com`, false},
		{"quoted local part naming the other domain", `"a@y.com"@x.com`, false},
		{"CRLF injection", "a@x.com\r\nBcc: b@y.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, user.IsEmailDomainAllowed(tc.email, allowed))
		})
	}
}

// An address ValidateEmail will not store is judged as it always was, by what
// follows the first at sign. It has to be: OIDC runs this check on every
// login, so refusing an address that merely needs its quotes (#389) would lock
// out an existing account whose IdP sends it. Nothing is lost by admitting it
// here, because a new account is still refused at Create (next test).
func TestIsEmailDomainAllowed_AnAddressThatCannotBeStoredIsJudgedAsBefore(t *testing.T) {
	require.True(t, user.IsEmailDomainAllowed(`"a b"@x.com`, []string{"x.com"}))
	require.False(t, user.IsEmailDomainAllowed(`"a b"@y.com`, []string{"x.com"}))
}

func TestUpsertSAMLUser_ANewAccountWithAnAddressThatCannotBeStoredIsRefused(t *testing.T) {
	store := newFakeUserStore()
	svc := user.NewService(store)

	_, err := svc.UpsertSAMLUser(context.Background(),
		"saml-quoted", `"a b"@example.com`, "Quoted", []string{"example.com"})
	require.Error(t, err)
	require.Equal(t, 0, store.creates)
}

func TestIsEmailDomainAllowed_TheListIsTrimmedAndCaseFolded(t *testing.T) {
	require.True(t, user.IsEmailDomainAllowed("<a@x.com>", []string{"y.com", "  X.COM  "}))
	require.False(t, user.IsEmailDomainAllowed("<a@z.com>", []string{"y.com", "  X.COM  "}))
}

// An empty list is the unrestricted setting and does not look at the address.
func TestIsEmailDomainAllowed_EmptyListIsUnrestricted(t *testing.T) {
	require.True(t, user.IsEmailDomainAllowed("a@x.com", nil))
	require.True(t, user.IsEmailDomainAllowed("not an address", []string{}))
}

func TestUpsertSAMLUser_BracketedAddressInAnAllowedDomain(t *testing.T) {
	store := newFakeUserStore()
	svc := user.NewService(store)

	got, err := svc.UpsertSAMLUser(context.Background(),
		"saml-bracketed", "<Person@Example.COM>", "Bracketed", []string{"example.com"})
	require.NoError(t, err)
	require.Equal(t, "person@example.com", got.Email, "the account holds the bare address")
}

func TestUpsertSAMLUser_BracketedAddressInADisallowedDomainIsStillRefused(t *testing.T) {
	store := newFakeUserStore()
	svc := user.NewService(store)

	_, err := svc.UpsertSAMLUser(context.Background(),
		"saml-bracketed", "<person@other.test>", "Bracketed", []string{"example.com"})
	require.ErrorIs(t, err, user.ErrDomainNotAllowed)
	require.Equal(t, 0, store.creates)
}
