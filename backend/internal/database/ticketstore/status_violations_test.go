package ticketstore

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestIsStatusNameViolation pins #278: detection must go through the typed
// *pgconn.PgError, not a string match against err.Error(), so it survives
// being wrapped and does not depend on any driver's exact message format —
// shaped like TestIsDuplicateLinkViolation (#195).
func TestIsStatusNameViolation(t *testing.T) {
	dup := &pgconn.PgError{Code: "23505", ConstraintName: "statuses_name_key"}

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"raw duplicate constraint violation", dup, true},
		{
			"wrapped in additional context (fmt.Errorf %w)",
			fmt.Errorf("creating status: %w", dup),
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
			&pgconn.PgError{Code: "23503", ConstraintName: "statuses_name_key"},
			false,
		},
		{"an unrelated plain error", errors.New("connection reset by peer"), false},
		{"nil", nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isStatusNameViolation(tc.err)
			if got != tc.want {
				t.Errorf("isStatusNameViolation(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestIsStatusInUseViolation pins #279's race backstop: the shared-transaction
// test harness cannot survive a real 23503 (a failed statement aborts the
// whole transaction), so this is the only practical coverage of the detection
// logic, shaped like TestIsPolicyInUseViolation (#261).
func TestIsStatusInUseViolation(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			"tickets.status_id foreign key",
			&pgconn.PgError{Code: "23503", ConstraintName: "tickets_status_id_fkey"},
			true,
		},
		{
			"ticket_status_history.from_status_id foreign key",
			&pgconn.PgError{Code: "23503", ConstraintName: "ticket_status_history_from_status_id_fkey"},
			true,
		},
		{
			"ticket_status_history.to_status_id foreign key",
			&pgconn.PgError{Code: "23503", ConstraintName: "ticket_status_history_to_status_id_fkey"},
			true,
		},
		{
			"wrapped in additional context (fmt.Errorf %w)",
			fmt.Errorf("deleting status: %w", &pgconn.PgError{Code: "23503", ConstraintName: "tickets_status_id_fkey"}),
			true,
		},
		{
			"same code, unrelated constraint — must not match",
			&pgconn.PgError{Code: "23503", ConstraintName: "some_other_constraint"},
			false,
		},
		{
			"same constraint name, different code — must not match",
			&pgconn.PgError{Code: "23505", ConstraintName: "tickets_status_id_fkey"},
			false,
		},
		{"an unrelated plain error", errors.New("connection reset by peer"), false},
		{"nil", nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isStatusInUseViolation(tc.err)
			if got != tc.want {
				t.Errorf("isStatusInUseViolation(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
