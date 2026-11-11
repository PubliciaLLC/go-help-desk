package ratelimit

import (
	"context"
	"time"
)

// Tarpit slows an over-budget key down. It never refuses one.
//
// Password verification runs before the counter is consulted — deliberately,
// so that a correct password is always honoured and nobody can be locked out
// of their own account by somebody else's guessing — so the counter alone only
// changed the status code on a wrong guess. An attacker who ignored 429 had
// unlimited online guesses, bounded by bcrypt alone.
//
// What this buys, measured through the real login handler on a ten-core
// machine with a one-second delay:
//
//	connections   with tarpit   without
//	          1        ~1/sec    22/sec
//	          8         9/sec   132/sec
//	        256       154/sec   154/sec
//
// So it costs a serial client about twenty times, and against something that
// opens a couple of hundred connections it costs nothing at all — at that
// point bcrypt is the only thing left, which is where this started. The bound
// is connections divided by the delay, not one over the delay, and saying
// otherwise would be describing a control that is not there. MFA is the
// answer to a determined parallel attacker; this is the answer to a script.
//
// Two rules, and the second is why the number above is not better.
//
// One turn at a time per key, so a serial caller is slowed by the delay
// rather than just made to wait once.
//
// And nobody waits longer than twice the delay. The first version bounded the
// queue instead and refused anything past it, which handed an attacker the
// lockout this ordering exists to prevent: eight connections against a known
// email address, and the owner's CORRECT password came back 429. A request
// that cannot get its turn in time now goes ahead without one. That is what
// flattens the table above at high concurrency, and it is the right trade:
// the guarantee that nobody can be kept out of their own account is worth
// more than a bound that a determined attacker walks around anyway.
func (l *Limiter) Tarpit(ctx context.Context, key string, delay time.Duration) {
	if l.limit <= 0 || delay <= 0 {
		return
	}

	l.mu.Lock()
	if l.slots == nil {
		l.slots = make(map[string]*tarpitSlot)
	}
	slot, ok := l.slots[key]
	if !ok {
		slot = &tarpitSlot{turn: make(chan struct{}, 1)}
		l.slots[key] = slot
	}
	slot.waiting++
	l.mu.Unlock()

	// Dropped once the last waiter leaves. Keys are partly attacker-chosen --
	// any email address can be submitted -- so a map that only grows is a leak.
	defer func() {
		l.mu.Lock()
		slot.waiting--
		if slot.waiting == 0 {
			delete(l.slots, key)
		}
		l.mu.Unlock()
	}()

	wait := time.NewTimer(delay)
	defer wait.Stop()

	select {
	case slot.turn <- struct{}{}:
		// Our turn. Hold it for the delay, then hand it on.
		defer func() { <-slot.turn }()
	case <-wait.C:
		// Somebody else has it and our patience is spent. Going ahead
		// unthrottled is the deliberate choice; see above.
		return
	case <-ctx.Done():
		return
	}

	hold := time.NewTimer(delay)
	defer hold.Stop()
	select {
	case <-hold.C:
	case <-ctx.Done():
	}
}

// tarpitSlot is one key's queue: a one-deep channel that makes the waits
// sequential, and a count so the slot can be freed when it empties.
type tarpitSlot struct {
	turn    chan struct{}
	waiting int
}
