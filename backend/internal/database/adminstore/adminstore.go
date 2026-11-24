// Package adminstore implements domain/admin.Store (settings) against PostgreSQL.
package adminstore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
)

// Store implements admin.Store.
type Store struct{ q *dbgen.Queries }

// New returns a Store backed by the given Queries.
func New(q *dbgen.Queries) *Store { return &Store{q: q} }

func (s *Store) Get(ctx context.Context, key string) ([]byte, error) {
	row, err := s.q.GetSetting(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("getting setting %q: %w", key, err)
	}
	return row, nil
}

func (s *Store) Set(ctx context.Context, key string, value []byte) error {
	return s.q.SetSetting(ctx, dbgen.SetSettingParams{
		Key:   key,
		Value: json.RawMessage(value),
	})
}

func (s *Store) List(ctx context.Context) (map[string][]byte, error) {
	rows, err := s.q.ListSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing settings: %w", err)
	}
	out := make(map[string][]byte, len(rows))
	for _, r := range rows {
		out[r.Key] = []byte(r.Value)
	}
	return out, nil
}
