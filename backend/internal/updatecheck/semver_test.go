package updatecheck

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewer(t *testing.T) {
	for _, tc := range []struct {
		name            string
		current, latest string
		want            bool
		wantErr         bool
	}{
		{"patch ahead", "1.3.0", "1.3.1", true, false},
		{"minor ahead", "1.3.9", "1.4.0", true, false},
		{"major ahead", "1.9.9", "2.0.0", true, false},
		{"same", "1.3.0", "1.3.0", false, false},
		{"older", "1.3.1", "1.3.0", false, false},
		{"numeric not lexical", "1.9.0", "1.10.0", true, false},
		{"v prefix on the tag", "1.3.0", "v1.3.1", true, false},
		{"v prefix on both", "v1.3.0", "v1.3.0", false, false},
		{"release beats its own beta", "1.3.0-beta", "1.3.0", true, false},
		{"beta is not newer than its release", "1.3.0", "1.3.0-beta", false, false},
		{"beta.2 beats beta.1", "1.3.0-beta.1", "1.3.0-beta.2", true, false},
		{"beta.10 beats beta.2 numerically", "1.3.0-beta.2", "1.3.0-beta.10", true, false},
		{"alpha before beta", "1.3.0-beta", "1.3.0-alpha", false, false},
		{"longer prerelease wins a tie", "1.3.0-beta", "1.3.0-beta.1", true, false},
		{"build metadata ignored", "1.3.0+abc", "1.3.0+def", false, false},
		{"garbage latest", "1.3.0", "latest", false, true},
		{"garbage current", "dev", "1.3.0", false, true},
		{"two parts", "1.3", "1.4.0", false, true},
		{"leading zeros", "1.3.0", "1.03.0", false, true},
		{"empty", "", "", false, true},
		{"empty prerelease identifier", "1.3.0", "1.3.1-", false, true},
		{"negative", "1.3.0", "1.-3.1", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Newer(tc.current, tc.latest)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
