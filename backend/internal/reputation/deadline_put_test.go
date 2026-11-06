package reputation_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
)

// ctxHonouringStore is a cache whose Put respects its context, the way a
// database driver does. The package's other fake ignores it, which is why this
// exists and why the defect it pins went unnoticed.
type ctxHonouringStore struct {
	mu    sync.Mutex
	puts  int
	delay time.Duration
	rows  map[string]reputation.Reputation
}

func (s *ctxHonouringStore) Get(_ context.Context, sha256, provider string) (reputation.Reputation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rep, ok := s.rows[sha256+"|"+provider]; ok {
		return rep, nil
	}
	return reputation.Reputation{}, reputation.ErrNotCached
}

func (s *ctxHonouringStore) Put(ctx context.Context, sha256, provider string, rep reputation.Reputation) error {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rows == nil {
		s.rows = map[string]reputation.Reputation{}
	}
	s.rows[sha256+"|"+provider] = rep
	s.puts++
	return nil
}

// A verdict that arrived inside the budget is kept, even when the budget runs
// out while it is being written.
//
// The deadline exists to stop a hung third party holding a page open, so it
// binds the call to the third party. Writing to our own database is not that
// call, and binding it too meant a verdict arriving just inside the budget was
// rendered, failed to cache, logged as a fault, and then fetched again on the
// next render — spending the operator's allowance a second time for an answer
// we already had.
//
// Narrow against a local Postgres and free to close, which is the argument for
// closing it rather than the argument for ignoring it.
func TestService_AVerdictInsideTheBudgetIsStillCached(t *testing.T) {
	store := &ctxHonouringStore{delay: 80 * time.Millisecond}
	svc := reputation.NewService(
		&fakeProvider{name: reputation.ProviderVirusTotal, rep: reputation.Reputation{
			State: reputation.Clean, Total: 70,
		}},
		store,
		reputation.NewBudget(),
	)
	// The provider answers at once; the store then takes longer than the
	// budget has left.
	svc.Deadline = time.Now().Add(30 * time.Millisecond)

	got, err := svc.GetOrLookup(context.Background(), eicarSHA)

	require.NoError(t, err,
		"the verdict arrived in time; keeping it is our own work and not the third party's")
	require.Equal(t, reputation.Clean, got.State)

	store.mu.Lock()
	defer store.mu.Unlock()
	require.Equal(t, 1, store.puts,
		"a verdict we paid for and received must not be thrown away by a clock "+
			"that exists to bound somebody else's server")
}
