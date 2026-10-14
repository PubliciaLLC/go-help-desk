package database

import (
	"context"
	"fmt"

	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
)

// SecretStore wraps the sqlc-generated methods for managing instance secrets.
type SecretStore struct {
	q *dbgen.Queries
}

// NewSecretStore creates a new SecretStore.
func NewSecretStore(q *dbgen.Queries) *SecretStore {
	return &SecretStore{q: q}
}

// InsertIfAbsent inserts a secret if it does not already exist.
func (s *SecretStore) InsertIfAbsent(ctx context.Context, name string, value []byte) error {
	if err := s.q.InsertInstanceSecretIfAbsent(ctx, dbgen.InsertInstanceSecretIfAbsentParams{
		Name:  name,
		Value: value,
	}); err != nil {
		return fmt.Errorf("inserting instance secret: %w", err)
	}
	return nil
}

// Get retrieves a secret by name.
func (s *SecretStore) Get(ctx context.Context, name string) ([]byte, error) {
	value, err := s.q.GetInstanceSecret(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("retrieving instance secret: %w", err)
	}
	return value, nil
}
