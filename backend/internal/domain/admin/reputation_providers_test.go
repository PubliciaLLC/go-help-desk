package admin_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// The per-provider reputation settings (#168), which replaced one selected
// provider and one shared key.

// Nothing is enabled on a fresh instance, and that is a supported
// configuration rather than an unfinished one: attachments are judged by this
// instance's own scanner alone.
func TestReputationEnabled_NothingIsOnByDefault(t *testing.T) {
	svc := admin.NewService(newFakeAdminStore())
	ctx := context.Background()

	for _, p := range admin.ReputationProviders() {
		require.False(t, svc.ReputationEnabled(ctx, p), "%s is on with nobody having said so", p)
	}
	require.Empty(t, svc.EnabledReputationProviders(ctx))
}

// Each toggle moves exactly one provider, which is the thing the single
// setting could not do.
func TestReputationEnabled_EachToggleIsItsOwnProvider(t *testing.T) {
	ctx := context.Background()

	for _, on := range admin.ReputationProviders() {
		t.Run(on, func(t *testing.T) {
			svc := admin.NewService(newFakeAdminStore())
			enabledKey, _, ok := admin.ReputationSettingKeys(on)
			require.True(t, ok)
			require.NoError(t, svc.SetRaw(ctx, enabledKey, []byte(`true`)))

			for _, p := range admin.ReputationProviders() {
				require.Equal(t, p == on, svc.ReputationEnabled(ctx, p),
					"enabling %s changed %s", on, p)
			}
			require.Equal(t, []string{on}, svc.EnabledReputationProviders(ctx))
		})
	}
}

// Several at once, in ProviderNames order whatever order they were written in.
//
// Ordered rather than a set because the order decides which provider's answer
// summarises the attachment row when two are equally serious, and an order
// that came out of map iteration would make that summary change between
// renders of the same page.
func TestEnabledReputationProviders_AreInCanonicalOrder(t *testing.T) {
	svc := admin.NewService(newFakeAdminStore())
	ctx := context.Background()

	for _, p := range []string{"circl", "virustotal", "polyswarm"} {
		enabledKey, _, _ := admin.ReputationSettingKeys(p)
		require.NoError(t, svc.SetRaw(ctx, enabledKey, []byte(`true`)))
	}

	require.Equal(t, []string{"virustotal", "polyswarm", "circl"},
		svc.EnabledReputationProviders(ctx))
}

// A key per provider, which the single key could not do: switching services
// used to destroy the one you had already pasted, and an operator holding two
// had to choose.
func TestReputationKey_IsPerProvider(t *testing.T) {
	svc := admin.NewService(newFakeAdminStore())
	ctx := context.Background()

	require.NoError(t, svc.SetRaw(ctx, admin.KeyAttachmentReputationVirusTotalKey, []byte(`"vt-key"`)))
	require.NoError(t, svc.SetRaw(ctx, admin.KeyAttachmentReputationMetaDefenderKey, []byte(`"md-key"`)))

	require.Equal(t, "vt-key", svc.ReputationKey(ctx, "virustotal"))
	require.Equal(t, "md-key", svc.ReputationKey(ctx, "metadefender"))
	require.Empty(t, svc.ReputationKey(ctx, "polyswarm"), "a key nobody set is not another provider's")
}

// CIRCL has no key setting at all, so there is nothing to read and nothing an
// operator could paste into it.
func TestReputationKey_CIRCLHasNone(t *testing.T) {
	svc := admin.NewService(newFakeAdminStore())
	ctx := context.Background()

	_, apiKeyKey, ok := admin.ReputationSettingKeys("circl")
	require.True(t, ok)
	require.Empty(t, apiKeyKey, "an empty box an operator feels obliged to fill is worse than no box")
	require.Empty(t, svc.ReputationKey(ctx, "circl"))
}

// A pasted key picks up a trailing newline often enough to matter, and a
// setting holding nothing but spaces has to read as unconfigured rather than
// as a key the provider will reject.
func TestReputationKey_IsTrimmed(t *testing.T) {
	svc := admin.NewService(newFakeAdminStore())
	ctx := context.Background()

	require.NoError(t, svc.SetRaw(ctx, admin.KeyAttachmentReputationVirusTotalKey, []byte(`"  vt-key\n"`)))
	require.Equal(t, "vt-key", svc.ReputationKey(ctx, "virustotal"))

	require.NoError(t, svc.SetRaw(ctx, admin.KeyAttachmentReputationPolySwarmKey, []byte(`"   "`)))
	require.Empty(t, svc.ReputationKey(ctx, "polyswarm"))
}

// A name this build does not know reads as nothing configured rather than as a
// lookup against settings that do not exist.
func TestReputationSettings_AnUnknownProviderIsNothing(t *testing.T) {
	svc := admin.NewService(newFakeAdminStore())
	ctx := context.Background()

	_, _, ok := admin.ReputationSettingKeys("hybridanalysis")
	require.False(t, ok)
	require.False(t, svc.ReputationEnabled(ctx, "hybridanalysis"))
	require.Empty(t, svc.ReputationKey(ctx, "hybridanalysis"))
}

// All seven settings are session-gated. A leaked API key must not be able to
// turn a provider on — that decides where customers' file hashes go — nor to
// paste a key, which decides whether they go anywhere.
func TestAuthCriticalKeys_CoverEveryReputationSetting(t *testing.T) {
	critical := map[string]bool{}
	for _, k := range admin.AuthCriticalKeys() {
		critical[k] = true
	}

	for _, p := range admin.ReputationProviders() {
		enabledKey, apiKeyKey, ok := admin.ReputationSettingKeys(p)
		require.True(t, ok)
		require.True(t, critical[enabledKey], "%s is not session-gated", enabledKey)
		if apiKeyKey != "" {
			require.True(t, critical[apiKeyKey], "%s is not session-gated", apiKeyKey)
		}
	}
}
