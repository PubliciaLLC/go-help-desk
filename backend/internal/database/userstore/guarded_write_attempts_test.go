package userstore

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

// guardedWrite never touches the Store's fields, so these need no database:
// they pin how many times it asks, which a real deadlock cannot be made to
// pin (see TestGuardedWrite_SurvivesTwoDeadlocks for the real thing).
func TestGuardedWrite_Attempts(t *testing.T) {
	deadlock := &pgconn.PgError{Code: "40P01"}
	other := errors.New("connection reset")

	tests := []struct {
		name        string
		answers     []error // one per attempt; the last repeats if asked again
		wantCalls   int
		wantApplied bool
		wantErr     error // matched with errors.Is; nil means no error
	}{
		{"first try succeeds", []error{nil}, 1, true, nil},
		{"one deadlock then success", []error{deadlock, nil}, 2, true, nil},
		{"two deadlocks then success", []error{deadlock, deadlock, nil}, 3, true, nil},
		{"third deadlock in a row is returned, not retried", []error{deadlock}, 3, false, deadlock},
		{"a non-deadlock error is not retried", []error{other}, 1, false, other},
		{"a non-deadlock error after a deadlock is not retried again", []error{deadlock, other}, 2, false, other},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			applied, err := (&Store{}).guardedWrite(context.Background(), func() (uuid.UUID, error) {
				i := min(calls, len(tc.answers)-1)
				calls++
				return uuid.Nil, tc.answers[i]
			})
			require.Equal(t, tc.wantCalls, calls)
			require.Equal(t, tc.wantApplied, applied)
			if tc.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.wantErr)
			}
		})
	}
}

func TestGuardedWrite_CancelledContextStopsTheRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	_, err := (&Store{}).guardedWrite(ctx, func() (uuid.UUID, error) {
		calls++
		cancel() // gone after the first kill: no reason to wait for a second go
		return uuid.Nil, &pgconn.PgError{Code: "40P01"}
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, calls)
}
