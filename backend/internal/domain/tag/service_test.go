package tag

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

// notFoundStore is a Store whose delete and restore find nothing.
type notFoundStore struct{ Store }

func (notFoundStore) SoftDelete(_ context.Context, id uuid.UUID) error {
	return fmt.Errorf("tag %s: %w", id, ErrNotFound)
}

func (notFoundStore) Restore(_ context.Context, id uuid.UUID) error {
	return fmt.Errorf("tag %s: %w", id, ErrNotFound)
}

// #528: the store says a tag was not there, and the service lets that reach
// the handler, which answers 404 off the sentinel.
func TestDeleteAndRestore_UnknownTagIsErrNotFound(t *testing.T) {
	svc := NewService(notFoundStore{})
	ctx := context.Background()

	for name, err := range map[string]error{
		"delete":  svc.Delete(ctx, uuid.New()),
		"restore": svc.Restore(ctx, uuid.New()),
	} {
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: got %v, want ErrNotFound", name, err)
		}
	}
}
