package server_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// A hung third party must never hold a help desk handler open (#168).
//
// The rule is in DESIGN.md and it was written when there was one provider. It
// stopped holding the moment there were four: the lookups run one after
// another, per attachment, inside the request, and each provider's HTTP client
// gives a dead endpoint fifteen seconds. "unavailable" is never cached, so the
// whole thing repeats on every render.
//
// The failure that matters is not a refused connection — that is instant. It
// is an outbound firewall dropping packets, which is what this test's provider
// does: it accepts the connection and never answers. Two enabled providers and
// three quarantined attachments on one ticket was 6 x 15s = 90s of a single
// GET, against an http.Server whose WriteTimeout is 30s (cmd/server/main.go).
// Staff do not see a slow page in that state; they see the attachment list
// fail to load, every time, until the provider comes back.
//
// So the bound has to cover the WHOLE request, not one lookup and not one
// attachment, and the exhausted bound has to render: "unavailable" beside
// every provider, a 200, and a list that draws.
func TestListAttachments_HungProvidersCannotOutlastTheWriteTimeout(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	// The provider that accepts and never answers. It returns only when the
	// caller gives up (its request context is cancelled) or when the test
	// releases it.
	block := make(chan struct{})
	rig := newVerdictRig(t, h, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	})
	// Registered after the rig, so it runs BEFORE the rig's httptest.Close:
	// Close waits for outstanding handlers, and a handler still parked here
	// would deadlock the cleanup rather than fail the test.
	t.Cleanup(func() { close(block) })

	// Two providers, both pointed at the hung endpoint, both with a key so
	// neither is skipped for want of one.
	for _, k := range []string{
		admin.KeyAttachmentReputationVirusTotalKey,
		admin.KeyAttachmentReputationMetaDefenderKey,
	} {
		require.NoError(t, h.adminSvc.SetRaw(ctx, k, []byte(`"key-0123456789"`)))
	}
	for _, k := range []string{
		admin.KeyAttachmentReputationVirusTotalEnabled,
		admin.KeyAttachmentReputationMetaDefenderEnabled,
	} {
		require.NoError(t, h.adminSvc.SetRaw(ctx, k, []byte(`true`)))
	}

	tk := rig.ticketFor(t, h.staffID)
	const attachments = 3
	for _, name := range []string{"one.exe.zip", "two.exe.zip", "three.exe.zip"} {
		rig.attach(t, tk, name)
	}

	// Comfortably inside the 30s WriteTimeout, and far enough from it that the
	// DB work and the JSON write still have the request to themselves. The
	// number is not the contract; "bounded, whatever is enabled and however
	// many files there are" is.
	const bound = 10 * time.Second

	start := time.Now()
	got := rig.list(t, h.apiKey, tk)
	elapsed := time.Since(start)

	t.Logf("list of %d quarantined attachments with 2 hung providers: %v", attachments, elapsed)
	require.Less(t, elapsed, bound,
		"%d attachments x 2 hung providers took %v; the whole request's reputation "+
			"work has to be bounded, not each lookup", attachments, elapsed)

	require.Len(t, got, attachments)
	for _, a := range got {
		require.NotNil(t, a.Reputation,
			"a provider that was asked and could not answer is an entry, not a silence")
		require.Equal(t, "unavailable", a.Reputation.State,
			"an exhausted deadline is 'we do not know', never an error and never clean")
		require.Len(t, a.Reputation.Providers, 2,
			"both enabled providers are accounted for, even the one never reached")
		for _, p := range a.Reputation.Providers {
			require.Equal(t, "unavailable", p.State)
			require.Nil(t, p.Detected, "nothing was counted, so no counts")
			require.Nil(t, p.Total, "nothing was counted, so no counts")
		}
		require.NotNil(t, a.ReputationURL, "the link needs nobody to answer")
	}

	// And the request stopped asking rather than queueing more: the first
	// lookup spends the whole bound, so at most one more can have started.
	require.LessOrEqual(t, rig.hits.Load(), int64(2),
		"%d requests went out for a deadline only one of them could fit in", rig.hits.Load())
}
