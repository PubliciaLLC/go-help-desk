package admin_test

import (
	"context"
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

func TestHomeScreenName(t *testing.T) {
	cases := []struct {
		name      string
		siteName  []byte // raw JSON; nil: no row
		shortName []byte
		wantShort string
		wantHome  string
	}{
		{"nothing set", nil, nil, "", "Go Help Desk"},
		{"site name only", []byte(`"Acme IT"`), nil, "", "Acme IT"},
		{"short name set", []byte(`"Acme IT Help Desk"`), []byte(`"IT"`), "IT", "IT"},
		{"short name empty falls back", []byte(`"Acme IT"`), []byte(`""`), "", "Acme IT"},
		{"short name blank falls back", []byte(`"Acme IT"`), []byte(`"   "`), "", "Acme IT"},
		{"short name is trimmed", []byte(`"Acme IT"`), []byte(`"  IT  "`), "IT", "IT"},
		{"unreadable short name falls back", []byte(`"Acme IT"`), []byte(`42`), "", "Acme IT"},
		{"short name with the default site name", nil, []byte(`"Help"`), "Help", "Help"},
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
			require.Equal(t, tc.wantShort, svc.SiteShortName(context.Background()))
			require.Equal(t, tc.wantHome, svc.HomeScreenName(context.Background()))
		})
	}
}
