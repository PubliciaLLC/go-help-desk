package cannedresponse

import (
	"context"

	"github.com/google/uuid"
)

// Store is the persistence interface for canned responses.
type Store interface {
	Create(ctx context.Context, cr CannedResponse) error
	Get(ctx context.Context, id uuid.UUID) (CannedResponse, error)
	Update(ctx context.Context, cr CannedResponse) error
	Delete(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context) ([]CannedResponse, error)
}
