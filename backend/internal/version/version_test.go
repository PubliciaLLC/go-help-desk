package version_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/version"
)

// TestVersion_MatchesTheLatestTag catches the mistake v1.1.0 shipped with:
// the constant said 1.0.1, nothing overrode it at build time, and every
// deployment reported the wrong version in its footer and at /api/v1/site.
//
// The check is deliberately one-directional. It fails when the newest tag is
// AHEAD of the constant, which is the tagged-without-bumping case. It stays
// quiet while the constant is ahead of the tags, which is the normal state
// between bumping and tagging.
func TestVersion_MatchesTheLatestTag(t *testing.T) {
	out, err := exec.Command("git", "tag", "--list", "v*", "--sort=-v:refname").Output()
	if err != nil {
		t.Skip("git unavailable; nothing to compare against")
	}
	tags := strings.Fields(string(out))
	if len(tags) == 0 {
		t.Skip("no version tags yet")
	}

	latest := strings.TrimPrefix(tags[0], "v")
	require.NotEqual(t, "", version.Version)

	if latest != version.Version {
		t.Logf("latest tag %s, constant %s", latest, version.Version)
		require.Truef(t, isNewer(version.Version, latest),
			"version.Version (%s) is behind the newest tag (%s) — bump it, or the release "+
				"reports the wrong version as v1.1.0 did", version.Version, latest)
	}
}

// isNewer reports whether a is a later version than b, comparing numerically
// per component so 1.10.0 beats 1.9.0.
func isNewer(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if as[i] == bs[i] {
			continue
		}
		return atoi(as[i]) > atoi(bs[i])
	}
	return len(as) > len(bs)
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// TestVersion_IsNotAPlaceholder guards the other direction: a release must not
// ship the development default.
func TestVersion_IsNotAPlaceholder(t *testing.T) {
	require.NotContains(t, version.Version, "dev")
	require.NotEqual(t, "0.0.0", version.Version)
}
