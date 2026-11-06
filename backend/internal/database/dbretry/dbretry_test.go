package dbretry_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/dbretry"
)

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"deadlock", &pgconn.PgError{Code: "40P01"}, true},
		{"serialization failure", &pgconn.PgError{Code: "40001"}, true},
		{"wrapped", fmt.Errorf("writing: %w", &pgconn.PgError{Code: "40P01"}), true},
		{"unique violation", &pgconn.PgError{Code: "23505"}, false},
		{"plain error", errors.New("connection reset"), false},
		{"nil", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, dbretry.IsRetryable(tc.err))
		})
	}
}

func TestDo_Attempts(t *testing.T) {
	deadlock := &pgconn.PgError{Code: "40P01"}
	other := errors.New("connection reset")

	tests := []struct {
		name      string
		answers   []error // one per attempt; the last repeats
		wantCalls int
		wantErr   error
	}{
		{"first try succeeds", []error{nil}, 1, nil},
		{"one deadlock then success", []error{deadlock, nil}, 2, nil},
		{"two deadlocks then success", []error{deadlock, deadlock, nil}, 3, nil},
		{"the third in a row is returned", []error{deadlock}, 3, deadlock},
		{"another error is not retried", []error{other}, 1, other},
		{"another error after a deadlock is not retried again", []error{deadlock, other}, 2, other},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			err := dbretry.Do(context.Background(), func(context.Context) error { return nil }, func() error {
				i := min(calls, len(tc.answers)-1)
				calls++
				return tc.answers[i]
			})
			require.Equal(t, tc.wantCalls, calls)
			if tc.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.wantErr)
			}
		})
	}
}

func TestDo_ACancelledContextStopsTheRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := dbretry.Do(ctx, nil, func() error {
		calls++
		cancel()
		return &pgconn.PgError{Code: "40P01"}
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, calls)
}
