package main

import (
	"errors"
	"fmt"
	"slices"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// outboxChannels are the channels requests queue notifications for (#164).
// "verification" carries signup verification email (#348); nothing else
// queues for it.
func outboxChannels() []string {
	return []string{"email", "webhook", "verification"}
}

// checkOutboxWiring refuses to start when signup verification email could
// never be sent (#361). The outbox dispatcher skips a channel it does not
// list and returns nil, so without "verification" in queued every signup
// would answer 202 and mail nothing, with no error anywhere. A queued channel
// with no sender would have its rows failed by the worker instead.
func checkOutboxWiring(queued []string, senders map[string]notification.Dispatcher) error {
	if !slices.Contains(queued, "verification") {
		return errors.New(`the outbox does not queue the "verification" channel, so signup verification email would never be sent`)
	}
	for _, ch := range queued {
		if senders[ch] == nil {
			return fmt.Errorf("outbox channel %q has no sender", ch)
		}
	}
	return nil
}
