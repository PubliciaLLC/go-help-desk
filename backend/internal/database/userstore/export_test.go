package userstore

import "context"

// SetRetryPause lets the external tests gate guardedWrite's retries on the
// state of a concurrent transaction. See Store.retryPause.
func (s *Store) SetRetryPause(f func(ctx context.Context) error) { s.retryPause = f }
