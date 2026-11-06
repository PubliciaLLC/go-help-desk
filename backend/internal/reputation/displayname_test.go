package reputation_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// An unrecognised provider has no display name, and that is the answer rather
// than a gap.
//
// The old version returned the raw setting value, which puts whatever an
// operator typed into a sentence attributing a claim — "xyzzy has never seen
// this file" — which reads as a service that exists and has an opinion. An
// empty name lets the caller fall back to unattributed wording, which is the
// truth: we do not know who said it, so we do not say.
//
// Unreachable while the settings handler refuses an unknown provider. That
// validation was itself missing until an adversarial review found it, which
// is the argument for the function being correct on its own rather than
// relying on the caller.
func TestDisplayName(t *testing.T) {
	cases := []struct{ provider, want string }{
		{reputation.ProviderVirusTotal, "VirusTotal"},
		{reputation.ProviderMetaDefender, "MetaDefender"},
		{"", ""},
		{"virus-total", ""},
		{"VirusTotal", ""},     // the display name is not an identifier
		{"hybridanalysis", ""}, // a real service we do not implement
		{"<script>x</script>", ""},
	}

	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			require.Equal(t, tc.want, reputation.DisplayName(tc.provider))
		})
	}
}
