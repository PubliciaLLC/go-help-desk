package reputation_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// ---------------------------------------------------------------------------
// The rule CIRCL forces.
//
// The key used to be the on switch, and for the three commercial providers it
// still is: no key, no lookup. CIRCL needs no key ever, so that rule alone
// would leave it permanently disabled — the lookup was refused on an empty key
// before anything had asked which provider was configured.
//
// The rule is now: a lookup runs when the configured provider CAN run. A key
// for the three commercial ones, nothing for CIRCL. Which removes a special
// case rather than adding one, and leaves today's behaviour untouched:
// virustotal with no key is still a link and no lookup.
// ---------------------------------------------------------------------------

func TestCanLookup(t *testing.T) {
	const key = "a-key"

	cases := []struct {
		name     string
		provider string
		apiKey   string
		want     bool
		why      string
	}{
		{"virustotal with a key", reputation.ProviderVirusTotal, key, true, ""},
		{"virustotal with none", reputation.ProviderVirusTotal, "", false,
			"today's behaviour must not change: a commercial provider with no key is a link and no lookup"},
		{"metadefender with a key", reputation.ProviderMetaDefender, key, true, ""},
		{"metadefender with none", reputation.ProviderMetaDefender, "", false, ""},
		{"polyswarm with a key", reputation.ProviderPolySwarm, key, true, ""},
		{"polyswarm with none", reputation.ProviderPolySwarm, "", false, ""},

		{"circl with no key", reputation.ProviderCIRCL, "", true,
			"CIRCL needs no key ever; requiring one leaves it permanently disabled"},
		{"circl with a key nobody asked for", reputation.ProviderCIRCL, key, true,
			"a key it cannot use is not a reason to refuse the lookup"},

		{"nothing configured", "", key, false, ""},
		{"a provider we cannot talk to", "hybridanalysis", key, false,
			"an unknown provider has no client, no budget and no terms anybody accepted"},
		{"a display name is not an identifier", "CIRCL", "", false, ""},
		{"a spelling the setting refuses", "hashlookup", "", false, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, reputation.CanLookup(tc.provider, tc.apiKey), tc.why)
		})
	}
}
