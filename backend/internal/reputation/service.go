package reputation

import (
	"context"
	"errors"
	"fmt"
)

// ErrNotCached is what a Store returns when it holds no verdict for a
// (hash, provider) pair. A miss must be distinguishable from a verdict: a
// caller that cannot tell "no answer" from "an answer of clean" renders the
// first as the second, which is the defect this whole package exists to
// prevent.
var ErrNotCached = errors.New("reputation: no cached verdict")

// Store is the verdict cache, keyed by hash and provider.
//
// Narrow on purpose — two methods crossing a package boundary — so that the
// database adapter can live in internal/database without this package
// importing it.
type Store interface {
	Get(ctx context.Context, sha256, provider string) (Reputation, error)
	Put(ctx context.Context, sha256, provider string, rep Reputation) error
}

// Service is the get-or-lookup orchestration: the cache, the budget and one
// provider, in that order.
type Service struct {
	provider Provider
	store    Store
	budget   *Budget
}

// NewService wires a provider to its cache and its budget.
func NewService(provider Provider, store Store, budget *Budget) *Service {
	return &Service{provider: provider, store: store, budget: budget}
}

// GetOrLookup returns the stored verdict for sha256, fetching one if there is
// none and the budget allows it.
//
// A stored verdict is never re-fetched. Refreshing one is an explicit action,
// not something a page render does.
func (s *Service) GetOrLookup(ctx context.Context, sha256 string) (Reputation, error) {
	provider := s.provider.Name()

	rep, err := s.store.Get(ctx, sha256, provider)
	switch {
	case err == nil:
		return rep, nil
	case !errors.Is(err, ErrNotCached):
		return unavailable(fmt.Errorf("reading cached verdict: %w", err))
	}

	if err := s.budget.Spend(provider); err != nil {
		// The page says "not checked yet" and renders; an exhausted budget is
		// not an error the reader needs to see.
		return unavailable(err)
	}

	rep, err = s.provider.Lookup(ctx, sha256)
	if err == nil && !rep.State.known() {
		// A state outside the five is refused here rather than anywhere else,
		// because this is the only point in front of both the column and the
		// screen.
		//
		// The database CHECK on attachment_reputation.state protects the
		// column and nothing more. Without this, a provider returning a zero
		// value — State "" — has the write refused by the constraint and the
		// verdict returned to the caller anyway, where nothing has a branch
		// for the empty string and it renders as though the file were fine.
		// The constraint would have produced exactly the outcome it exists to
		// prevent.
		//
		// Today both providers return one of the five and a test pins that, so
		// this guards a future provider or a forgotten return path rather than
		// a live bug. It is one comparison, and the failure it prevents is a
		// file silently shown as safe.
		err = fmt.Errorf("provider %s returned an unknown state %q", provider, rep.State)
	}
	if err != nil || rep.State == Unavailable {
		// Unavailable is never written. A verdict is kept forever, so caching
		// a transient failure would freeze it permanently: a file whose scan
		// was in progress the first time anyone looked would read "no answer"
		// for the life of the instance. The next lookup tries again, which is
		// what makes a rate-limited render cost nothing permanent.
		return unavailable(err)
	}

	if err := s.store.Put(ctx, sha256, provider, rep); err != nil {
		// The verdict is good even though we failed to keep it, so it is
		// returned; the operator still gets the reason the cache did not take.
		return rep, fmt.Errorf("caching verdict: %w", err)
	}
	return rep, nil
}
