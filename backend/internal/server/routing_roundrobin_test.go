package server_test

import (
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Round-robin hands out turns atomically: creates that overlap each take their
// own slot. N overlapping creates over N candidates go to N different people.
// (The counter is the thing under test, so it is driven directly: the HTTP
// harness runs inside one shared transaction and cannot overlap requests.)
func TestRoundRobin_OverlappingCreatesTakeDifferentTurns(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	const n = 8
	candidates := make([]uuid.UUID, n)
	for i := range candidates {
		candidates[i] = uuid.New()
	}

	for round := range 20 {
		got := make(chan uuid.UUID, n)
		var wg sync.WaitGroup
		for range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got <- h.srv.RoundRobinFirst(candidates)
			}()
		}
		wg.Wait()
		close(got)
		seen := map[uuid.UUID]bool{}
		for id := range got {
			seen[id] = true
		}
		require.Len(t, seen, n, "round %d: overlapping creates were offered the same person", round)
	}
}
