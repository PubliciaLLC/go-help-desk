package slastore

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestIsPolicyInUseViolation pins #261's race backstop: the shared-transaction
// test harness cannot survive a real 23503 (a failed statement aborts the
// whole transaction), so this is the only practical coverage of the
// detection logic, shaped like TestIsDuplicateLinkViolation (#195).
func TestIsPolicyInUseViolation(t *testing.T) {
	dup := &pgconn.PgError{Code: "23503", ConstraintName: "sla_records_policy_id_fkey"}

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"raw foreign key violation", dup, true},
		{
			"wrapped in additional context (fmt.Errorf %w)",
			fmt.Errorf("deleting SLA policy: %w", dup),
			true,
		},
		{
			"wrapped twice",
			fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", dup)),
			true,
		},
		{
			"same code, different constraint — must not match",
			&pgconn.PgError{Code: "23503", ConstraintName: "some_other_constraint"},
			false,
		},
		{
			"same constraint name, different code — must not match",
			&pgconn.PgError{Code: "23505", ConstraintName: "sla_records_policy_id_fkey"},
			false,
		},
		{"an unrelated plain error", errors.New("connection reset by peer"), false},
		{"nil", nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isPolicyInUseViolation(tc.err)
			if got != tc.want {
				t.Errorf("isPolicyInUseViolation(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
