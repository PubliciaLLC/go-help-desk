package reputation_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/singleflight"

	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// blockingProvider blocks every Lookup until release is closed, and reports
// whether the context it was called with was ever cancelled while it waited
// — which is exactly the signal a leader's cancelled request context would
// send if Group's collapsed call were still bound to it.
type blockingProvider struct {
	name    string
	rep     reputation.Reputation
	entered chan struct{}
	release chan struct{}

	mu        sync.Mutex
	calls     int
	sawCancel bool
}

func (p *blockingProvider) Name() string { return p.name }

func (p *blockingProvider) Lookup(ctx context.Context, sha256 string) (reputation.Reputation, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	close(p.entered)
	select {
	case <-p.release:
		return p.rep, nil
	case <-ctx.Done():
		p.mu.Lock()
		p.sawCancel = true
		p.mu.Unlock()
		return reputation.Reputation{}, ctx.Err()
	}
}

func (p *blockingProvider) LinkURL(string) string { return "" }

// TestGetOrLookup_OneCallersCancellationDoesNotAbortAnotherLiveCallers pins a
// concurrency bug found by adversarial review of #178 (the singleflight
// collapsing fix): Group.Do runs its closure once per key, on whichever
// caller registers first, and every other concurrent caller for that key
// shares that one call's outcome. Passing the leader's own request context
// straight into that shared call meant the leader's disconnect — a closed
// tab, a client timeout — aborted an answer a completely different, still-
// live caller was waiting on, and discarded a verdict that had already
// arrived, so the next render paid for the lookup again.
//
// Two goroutines call GetOrLookup on the SAME uncached hash through the SAME
// Service (sharing one Group, as Server wires every Service it builds to
// for the life of the process). Goroutine A's context is cancelled while its
// call is in flight; goroutine B's context is context.Background() and is
// never touched. Both must still get the real verdict, and it must be
// cached — none of that may depend on A's fate.
func TestGetOrLookup_OneCallersCancellationDoesNotAbortAnotherLiveCallers(t *testing.T) {
	store := newFakeStore()
	provider := &blockingProvider{
		name:    "virustotal",
		rep:     reputation.Reputation{State: reputation.Clean},
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	budget := reputation.NewBudget()
	group := new(singleflight.Group)

	svc := reputation.NewService(provider, store, budget)
	svc.Group = group

	const hash = "cancel-test-hash"

	aCtx, cancelA := context.WithCancel(context.Background())
	bCtx := context.Background()

	var aRep, bRep reputation.Reputation
	var aErr, bErr error
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		aRep, aErr = svc.GetOrLookup(aCtx, hash)
	}()

	// Wait until the leader (whichever goroutine won) has genuinely entered
	// the provider call before B joins, so B is guaranteed to collapse onto
	// the SAME in-flight call rather than racing a fresh one.
	<-provider.entered

	go func() {
		defer wg.Done()
		bRep, bErr = svc.GetOrLookup(bCtx, hash)
	}()

	// Give B a moment to reach Group.Do and join as a follower before A is
	// cancelled — otherwise B might not have registered yet and this would
	// not test what it claims to.
	time.Sleep(20 * time.Millisecond)

	cancelA()
	// A's cancellation must not reach the shared call: confirm nothing
	// observes it within a window well past when a propagated cancellation
	// would have unblocked Lookup's select.
	select {
	case <-provider.release:
	case <-time.After(50 * time.Millisecond):
	}
	require.False(t, provider.sawCancel,
		"caller A's context cancellation reached the shared provider call — it must be detached")

	close(provider.release)
	wg.Wait()

	require.NoError(t, aErr)
	require.NoError(t, bErr)
	require.Equal(t, reputation.Clean, aRep.State, "caller A must still get the real verdict")
	require.Equal(t, reputation.Clean, bRep.State, "caller B must get the real verdict")

	require.Equal(t, 1, provider.calls, "the two callers must collapse into one outbound lookup")
	require.Len(t, store.written(), 1, "the verdict must be cached despite the leader's cancellation")
}
