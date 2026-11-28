package ticketstore

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestIsDuplicateLinkViolation pins #195: detection must go through the typed
// *pgconn.PgError, not a string match against err.Error(), so it survives
// being wrapped and does not depend on any driver's exact message format.
func TestIsDuplicateLinkViolation(t *testing.T) {
	dup := &pgconn.PgError{Code: "23505", ConstraintName: "ticket_links_unique"}

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"raw duplicate constraint violation", dup, true},
		{
			"wrapped in additional context (fmt.Errorf %w)",
			fmt.Errorf("creating ticket link: %w", dup),
			true,
		},
		{
			"wrapped twice",
			fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", dup)),
			true,
		},
		{
			"same code, different constraint — must not match",
			&pgconn.PgError{Code: "23505", ConstraintName: "some_other_constraint"},
			false,
		},
		{
			"same constraint name, different code — must not match",
			&pgconn.PgError{Code: "23503", ConstraintName: "ticket_links_unique"},
			false,
		},
		{"an unrelated plain error", errors.New("connection reset by peer"), false},
		{"nil", nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isDuplicateLinkViolation(tc.err)
			if got != tc.want {
				t.Errorf("isDuplicateLinkViolation(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
