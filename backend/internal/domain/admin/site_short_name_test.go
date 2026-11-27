package admin_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// The home screen name (#708): what a phone prints under the icon. Optional;
// empty or unset means the site name.

func TestValidSiteShortName(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"empty means the default", "", false},
		{"ordinary", "Help Desk", false},
		{"thirty characters", strings.Repeat("a", 30), false},
		{"thirty-one characters", strings.Repeat("a", 31), true},
		// Counted in characters, not bytes: an accented name is not half as long.
		{"thirty multi-byte characters", strings.Repeat("é", 30), false},
		{"thirty-one multi-byte characters", strings.Repeat("é", 31), true},
		{"a bell", "\x07", true},
		{"a newline", "Help\nDesk", true},
		{"a tab", "Help\tDesk", true},
		{"a C1 control", "Help\u0085Desk", true},
		// Format and separator characters draw nothing or move the text: a
		// right-to-left override makes a name read backwards, a zero-width
		// space makes two names that look the same, U+2028 breaks a line.
		{"a right-to-left override", "Help\u202eksed", true},
		{"a left-to-right isolate", "Help\u2066Desk", true},
		{"a zero-width space", "Help\u200bDesk", true},
		{"a zero-width joiner", "Help\u200dDesk", true},
		{"a byte order mark", "\ufeffHelp", true},
		{"a soft hyphen", "Help\u00adDesk", true},
		{"a line separator", "Help\u2028Desk", true},
		{"a paragraph separator", "Help\u2029Desk", true},
		{"an ordinary no-break space is not refused", "Help\u00a0Desk", false},
		{"an emoji is not refused", "Help \U0001F6A8", false},
		{"surrounding space is trimmed first", "  a  ", false},
		{"thirty characters padded with space", "  " + strings.Repeat("a", 30) + "  ", false},
		{"only space is empty", "   ", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := admin.ValidSiteShortName(tc.in)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestSiteShortNameKey(t *testing.T) {
	require.Equal(t, "site_short_name", admin.KeySiteShortName)
	require.True(t, admin.IsKnownKey(admin.KeySiteShortName))
	// A branding setting, not a credential: an API key with the settings scope
	// may change it, as it may the site name.
	require.NotContains(t, admin.AuthCriticalKeys(), admin.KeySiteShortName)
}

func TestSiteNames_HomeScreenName(t *testing.T) {
	cases := []struct {
		name      string
		siteName  []byte // raw JSON; nil: no row
		shortName []byte
		wantHome  string
	}{
		{"nothing set", nil, nil, "Go Help Desk"},
		{"site name only", []byte(`"Acme IT"`), nil, "Acme IT"},
		{"short name set", []byte(`"Acme IT Help Desk"`), []byte(`"IT"`), "IT"},
		{"short name empty falls back", []byte(`"Acme IT"`), []byte(`""`), "Acme IT"},
		{"short name blank falls back", []byte(`"Acme IT"`), []byte(`"   "`), "Acme IT"},
		{"short name is trimmed", []byte(`"Acme IT"`), []byte(`"  IT  "`), "IT"},
		{"unreadable short name falls back", []byte(`"Acme IT"`), []byte(`42`), "Acme IT"},
		{"short name with the default site name", nil, []byte(`"Help"`), "Help"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeAdminStore()
			if tc.siteName != nil {
				store.data[admin.KeySiteName] = tc.siteName
			}
			if tc.shortName != nil {
				store.data[admin.KeySiteShortName] = tc.shortName
			}
			svc := admin.NewService(store)
			_, home, _ := svc.SiteNames(context.Background())
			require.Equal(t, tc.wantHome, home)
		})
	}
}

func TestSiteName(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte // raw JSON; nil: no row
		want string
	}{
		{"unset", nil, "Go Help Desk"},
		{"set", []byte(`"Acme IT"`), "Acme IT"},
		{"empty", []byte(`""`), "Go Help Desk"},
		// A blank name would render an empty <title> and an empty manifest name.
		{"blank", []byte(`"   "`), "Go Help Desk"},
		{"tabs and newlines", []byte(`" \t\n "`), "Go Help Desk"},
		{"trimmed", []byte(`"  Acme IT  "`), "Acme IT"},
		{"unreadable", []byte(`42`), "Go Help Desk"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeAdminStore()
			if tc.raw != nil {
				store.data[admin.KeySiteName] = tc.raw
			}
			svc := admin.NewService(store)
			require.Equal(t, tc.want, svc.SiteName(context.Background()))
			name, _, _ := svc.SiteNames(context.Background())
			require.Equal(t, tc.want, name)
		})
	}
}

// failingGets makes every read fail, as a database that has stopped answering.
type failingGets struct{ *fakeAdminStore }

func (failingGets) Get(context.Context, string) ([]byte, error) {
	return nil, errors.New("injected read failure")
}

// SiteNames reads both names for the page shell and the manifest. It always
// answers with usable names; the error is for the caller to log, and unset is
// not an error (an instance that never set a home screen name is normal).
func TestSiteNames_ReadFailureAnswersWithDefaultsAndAnError(t *testing.T) {
	ctx := context.Background()

	_, _, err := admin.NewService(newFakeAdminStore()).SiteNames(ctx)
	require.NoError(t, err, "nothing set is not a failure")

	name, home, err := admin.NewService(failingGets{newFakeAdminStore()}).SiteNames(ctx)
	require.Error(t, err)
	require.Equal(t, "Go Help Desk", name)
	require.Equal(t, "Go Help Desk", home)

	store := newFakeAdminStore()
	store.data[admin.KeySiteName] = []byte(`"Acme IT"`)
	store.data[admin.KeySiteShortName] = []byte(`42`)
	name, home, err = admin.NewService(store).SiteNames(ctx)
	require.Error(t, err, "a damaged value is worth a log line too")
	require.Equal(t, "Acme IT", name)
	require.Equal(t, "Acme IT", home)
}
