package reputation

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ManualRefreshFloor is the shortest interval between two staff-triggered
// re-checks of the same hash.
//
// A floor and not an override: it applies whatever the configured interval
// says, including when that is "never", and it cannot be used to spend the
// daily allowance in a loop. Seven days is short enough to be useful to
// somebody wondering whether the world has caught up with a sample, and long
// enough that a bored reader clicking a button cannot burn an operator's
// quota.
const ManualRefreshFloor = 7 * 24 * time.Hour

// The two refusals a manual re-check can come back with. Both are ordinary
// outcomes rather than failures — a handler turns them into an explanation,
// not an incident.
var (
	// ErrTooSoon: this hash was checked inside the floor above.
	ErrTooSoon = errors.New("reputation: checked too recently")

	// ErrDetectionIsFinal: the stored verdict is one there is nothing to learn
	// from re-asking about — a detection, because engines do not un-flag a
	// file, or a "known" file, because a named feed's catalogue entry does not
	// decay either. Both would spend a lookup to be told the same thing.
	//
	// Named for the detection because that was the only final verdict when it
	// was written, and it is compared with errors.Is by the handler and by
	// tests that must keep passing. Renaming it would be a breaking change for
	// no gain.
	ErrDetectionIsFinal = errors.New("reputation: a detection is not re-checked")
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
	// Now is injected so that "is this verdict older than a fortnight" is
	// testable without waiting a fortnight. Nil means time.Now. Same field,
	// and the same reason, as Budget.Now.
	Now func() time.Time

	// RefreshAfter is how old a non-detected verdict may be before the next
	// GetOrLookup re-fetches it instead of returning it.
	//
	// Zero means never: automatic re-checking is off. Read it any other way —
	// as an age threshold of zero — and every verdict is stale on every
	// render, which is the opposite of what the operator asked for and spends
	// their allowance doing it. It comes from
	// admin.Service.ReputationRefreshInterval, per request, because the
	// setting can change while the server runs.
	RefreshAfter time.Duration

	provider Provider
	store    Store
	budget   *Budget
}

// NewService wires a provider to its cache and its budget.
func NewService(provider Provider, store Store, budget *Budget) *Service {
	return &Service{provider: provider, store: store, budget: budget}
}

// Provider is the name of the service this one asks, as stored in
// attachment_reputation.provider.
//
// Exported because the caller now holds several of these at once — one per
// enabled provider — and has to attribute each answer to the service that gave
// it. A caller tracking that in a parallel slice is a caller that can get the
// two out of step, which is a verdict rendered under the wrong provider's
// name.
func (s *Service) Provider() string { return s.provider.Name() }

// final reports whether a verdict is one there is nothing left to learn from
// re-asking about. Both automatic expiry and the manual re-check are decided
// on it, so the two cannot drift apart.
func final(s State) bool { return s == Detected || s == Known }

// GetOrLookup returns the stored verdict for sha256, fetching one if there is
// none, or if the one on file has passed RefreshAfter and the budget allows
// it.
//
// Expiry is the whole reason this is not a plain cache read. A verdict decays:
// new signatures catch old malware, and the sample nobody had submitted when
// we asked is precisely the one that gets submitted a week later. Two verdicts
// are the exception and never expire: a detection, because engines do not
// un-flag a file, and a "known" file, because a hash does not fall out of a
// vendor catalogue.
func (s *Service) GetOrLookup(ctx context.Context, sha256 string) (Reputation, error) {
	provider := s.provider.Name()

	stored, err := s.store.Get(ctx, sha256, provider)
	switch {
	case err == nil && !s.stale(stored):
		return stored, nil

	case err == nil:
		// Past its interval. The stored verdict stays the fallback: a
		// re-check that cannot be made — no budget, provider down — must not
		// delete a real answer from the page. "Clean, checked three weeks ago"
		// is worth more to the person reading it than "not checked yet", and
		// the row carries the date for them to judge it by.
		fresh, ferr := s.lookup(ctx, sha256)
		if fresh.State == Unavailable {
			return stored, ferr
		}
		return fresh, ferr

	case !errors.Is(err, ErrNotCached):
		return unavailable(fmt.Errorf("reading cached verdict: %w", err))
	}

	return s.lookup(ctx, sha256)
}

// Refresh re-checks one hash because a person asked, rather than because a
// page was rendered.
//
// Three refusals, and each has a reason: there is nothing to refresh on a file
// nobody has looked up (ErrNotCached — the ordinary lazy lookup covers that),
// nothing to learn from re-confirming a verdict that cannot change
// (ErrDetectionIsFinal — a detection or a "known" file), and nothing to be
// gained from asking twice inside a week (ErrTooSoon). The
// budget applies on top of all three, because a button is not a reason to risk
// getting an operator's API key banned.
//
// Every refusal hands back the verdict already on file, so a caller can still
// render what is known alongside the reason it did not change.
func (s *Service) Refresh(ctx context.Context, sha256 string) (Reputation, error) {
	provider := s.provider.Name()

	stored, err := s.store.Get(ctx, sha256, provider)
	switch {
	case errors.Is(err, ErrNotCached):
		return unavailable(err)
	case err != nil:
		return unavailable(fmt.Errorf("reading cached verdict: %w", err))
	}

	if final(stored.State) {
		return stored, ErrDetectionIsFinal
	}
	// A zero FetchedAt is a verdict whose age nobody recorded. It cannot come
	// from the database — attachment_reputation.fetched_at is NOT NULL — and
	// refusing a person on a timestamp that does not exist would make the
	// control dead with no way to tell why. The budget still applies.
	if !stored.FetchedAt.IsZero() {
		if age := s.now().Sub(stored.FetchedAt); age < ManualRefreshFloor {
			return stored, fmt.Errorf("reputation: checked %v ago: %w", age.Round(time.Minute), ErrTooSoon)
		}
	}

	fresh, ferr := s.lookup(ctx, sha256)
	if fresh.State == Unavailable {
		if ferr == nil {
			// The provider answered, and the answer was "no answer" — an
			// in-progress scan, say. Nothing is stored and nothing changed,
			// and the caller asked for an action rather than a render, so it
			// has to be told the action did not happen.
			ferr = fmt.Errorf("reputation: %s had no answer to the re-check", provider)
		}
		return stored, ferr
	}
	return fresh, ferr
}

// stale reports whether a stored verdict has passed its refresh interval.
//
// Four ways to be exempt, in order: automatic re-checking is off, the verdict
// is final, or nobody recorded when it was fetched. The last is not pedantry —
// read as "fetched in year one" it makes every such row stale on every render,
// which is the allowance-burning failure the cache exists to prevent.
//
// Two states are final. Detected, because engines do not un-flag a file. And
// Known, for the same reason pointing the other way: a hash does not fall out
// of NSRL's catalogue, and re-confirming a catalogue entry out of an allowance
// of sixty an hour is the lookup guaranteed to tell nobody anything.
func (s *Service) stale(rep Reputation) bool {
	switch {
	case s.RefreshAfter <= 0, final(rep.State), rep.FetchedAt.IsZero():
		return false
	}
	return s.now().Sub(rep.FetchedAt) >= s.RefreshAfter
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// lookup spends budget, asks the provider, and keeps what it said.
//
// Split out of GetOrLookup so that the manual re-check spends and stores by
// exactly the same rules; a second copy of this is a second place for
// "unavailable is never cached" to be forgotten.
func (s *Service) lookup(ctx context.Context, sha256 string) (Reputation, error) {
	provider := s.provider.Name()

	if err := s.budget.Spend(provider); err != nil {
		// The page says "not checked yet" and renders; an exhausted budget is
		// not an error the reader needs to see.
		return unavailable(err)
	}

	rep, err := s.provider.Lookup(ctx, sha256)
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
		// Today every provider returns one of the six and a test pins that, so
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
