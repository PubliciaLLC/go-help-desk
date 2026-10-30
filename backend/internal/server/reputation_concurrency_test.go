package server

import (
	"context"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/singleflight"
)

// Two problems found in the v1.3.0-beta whole-system review (#178), neither a
// safety issue and both measurable against an operator's API allowance:
//
//  1. Concurrent renders of the same uncached hash each spent a lookup —
//     nothing made ten simultaneous misses wait for the first answer.
//  2. reputationServices read four toggles, the refresh interval and up to
//     three keys once PER ATTACHMENT, though none of it can change inside one
//     request — a ticket with ten quarantined attachments spent eighty reads
//     to learn something that could not have changed since the first one.

// TestAddReputation_ConcurrentLookupsOfTheSameHashCollapse pins the first: ten
// goroutines racing addReputation on the same uncached hash must produce one
// outbound request, not ten, once the server carries a shared singleflight
// Group.
//
// newRepRig's bare server (see newBareServer) has no Group wired — none of
// the concurrency existed to protect against when that helper was written —
// so it is added here, the same way Server.New wires one for the life of the
// process.
func TestAddReputation_ConcurrentLookupsOfTheSameHashCollapse(t *testing.T) {
	// Every outbound request is held until the test lets it go, and the test
	// lets it go only once all ten callers are inside the singleflight Group.
	//
	// Holding back only the first request was not enough: nothing made the
	// other nine be there yet. A caller scheduled late reached the Group after
	// the leader had answered and the Group had forgotten the key, found no
	// call to join, and spent a second lookup — a gap in the test, not in the
	// product, and one that only showed on a loaded runner.
	const n = 10
	release := make(chan struct{})

	rig := newRepRig(t, func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(vtCleanBody))
	})
	rig.setKey(t, repTestKey)
	rig.srv.repGroup = new(singleflight.Group)
	// Safety net for a failed assertion: never leave handlers parked.
	var releaseOnce sync.Once
	letGo := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(letGo)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			att := quarantinedAttachment(repTestHash)
			rig.srv.addReputation(context.Background(), &att)
		}()
	}

	// One caller leads the call; the other n-1 must be parked waiting on it.
	// If collapse were broken the extra callers would each fire their own
	// request into the held handler instead, so a second hit ends the wait
	// too and the assertion below reports it.
	require.Eventually(t, func() bool {
		return rig.hits.Load() > 1 || singleflightWaiters() == n-1
	}, 10*time.Second, time.Millisecond,
		"the other callers never all joined the in-flight lookup")
	letGo()
	wg.Wait()

	require.Equal(t, int64(1), rig.hits.Load(),
		"ten concurrent misses on the same hash must produce one outbound lookup")
}

// singleflightWaiters counts goroutines of the collapse test that are blocked
// waiting on somebody else's in-flight singleflight call.
//
// singleflight exposes no count of its waiters, and a goroutine "about to
// call Do" is indistinguishable from one that has joined, so this reads the
// goroutine stacks: a caller that has joined sits in WaitGroup.Wait inside
// Group.Do, which it reaches only after registering as a duplicate of the
// leader's call. The leader is inside Do too but running the lookup, not in
// Wait, so it is not counted. If the runtime ever renders those frames
// differently the count stays at zero and the test times out — loudly,
// rather than passing without having held anyone.
func singleflightWaiters() int {
	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	count := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "TestAddReputation_ConcurrentLookupsOfTheSameHashCollapse") &&
			strings.Contains(g, "singleflight.(*Group).Do") &&
			strings.Contains(g, "sync.(*WaitGroup).Wait") {
			count++
		}
	}
	return count
}

// TestReputationServices_CachedOncePerRequest pins the second: a request-scoped
// cache installed the way reputationDeadline installs it means the services
// (and the settings reads that built them) are built once, no matter how many
// attachments ask for them.
func TestReputationServices_CachedOncePerRequest(t *testing.T) {
	rig := newRepRig(t, repRespond(http.StatusOK, vtCleanBody))
	rig.setKey(t, repTestKey)

	cached := context.WithValue(context.Background(), repServicesCacheKey{}, &repServicesCache{})

	first := rig.srv.reputationServices(cached)
	second := rig.srv.reputationServices(cached)
	require.Len(t, first, 1)
	require.Len(t, second, 1)
	require.Same(t, first[0], second[0],
		"the same request must reuse the built Service, not read settings and build a new one per call")

	// A context with no cache installed — direct callers, and every test
	// above this one — keeps building fresh every time, exactly as before
	// this cache existed.
	uncached := context.Background()
	third := rig.srv.reputationServices(uncached)
	fourth := rig.srv.reputationServices(uncached)
	require.Len(t, third, 1)
	require.Len(t, fourth, 1)
	require.NotSame(t, third[0], fourth[0],
		"a context with no per-request cache must not accidentally share state across unrelated calls")
}

// TestReputationServices_TwoRequestsDoNotShareTheCache guards the boundary the
// two tests above assume: the cache is per REQUEST, not per Server, so a
// second request rebuilds rather than serving the first request's stale
// answer.
func TestReputationServices_TwoRequestsDoNotShareTheCache(t *testing.T) {
	rig := newRepRig(t, repRespond(http.StatusOK, vtCleanBody))
	rig.setKey(t, repTestKey)

	req1 := context.WithValue(context.Background(), repServicesCacheKey{}, &repServicesCache{})
	req2 := context.WithValue(context.Background(), repServicesCacheKey{}, &repServicesCache{})

	svc1 := rig.srv.reputationServices(req1)
	svc2 := rig.srv.reputationServices(req2)
	require.Len(t, svc1, 1)
	require.Len(t, svc2, 1)
	require.NotSame(t, svc1[0], svc2[0], "two different requests must not share one built Service")

	// Disabling the provider between requests proves the second request read
	// the setting again rather than replaying the first request's cache.
	rig.enable(t)
	req3 := context.WithValue(context.Background(), repServicesCacheKey{}, &repServicesCache{})
	svc3 := rig.srv.reputationServices(req3)
	require.Empty(t, svc3, "a later request must see a setting changed after an earlier request's cache was built")
}
