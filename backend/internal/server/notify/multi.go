package notify

import (
	"context"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
)

// Multi fans an event out to multiple Dispatcher implementations. The first
// non-nil error is returned; remaining dispatchers are still called.
type Multi struct {
	dispatchers []notification.Dispatcher
}

// NewMulti returns a Multi that dispatches to all provided dispatchers.
func NewMulti(dispatchers ...notification.Dispatcher) *Multi {
	return &Multi{dispatchers: dispatchers}
}

func (m *Multi) Dispatch(ctx context.Context, event notification.Event) error {
	var first error
	for _, d := range m.dispatchers {
		if err := d.Dispatch(ctx, event); err != nil && first == nil {
			first = err
		}
	}
	return first
}
