package server

import (
	"context"
	"net/http"
	"sync"
	"testing"

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
	// Blocks the first request to arrive until every goroutine has started,
	// so all ten calls are guaranteed to be in flight together rather than
	// racing a fast handler that might answer #1 before #2 even reaches
	// GetOrLookup.
	const n = 10
	release := make(chan struct{})
	var arrived sync.WaitGroup
	arrived.Add(1)
	var once sync.Once

	rig := newRepRig(t, func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() {
			arrived.Done()
			<-release
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(vtCleanBody))
	})
	rig.setKey(t, repTestKey)
	rig.srv.repGroup = new(singleflight.Group)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			att := quarantinedAttachment(repTestHash)
			rig.srv.addReputation(context.Background(), &att)
		}()
	}

	// Confirm one request is genuinely blocked inside the handler before
	// releasing it, so the other nine really did have to wait on it rather
	// than each having already fired and returned.
	arrived.Wait()
	close(release)
	wg.Wait()

	require.Equal(t, int64(1), rig.hits.Load(),
		"ten concurrent misses on the same hash must produce one outbound lookup")
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
