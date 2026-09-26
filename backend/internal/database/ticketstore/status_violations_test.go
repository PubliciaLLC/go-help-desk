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

// TestIsStatusInUseViolation pins #279's race backstop: the three hand-typed
// constraint-name strings below, matched only against copies of themselves
// here. The genuinely untestable part is the concurrent RACE itself — two
// overlapping requests landing between RemoveStatus's counts and its delete —
// which cannot be reproduced deterministically. The constraint-name-matching
// logic that this test exercises is NOT similarly untestable: it can and is
// also exercised against a real Postgres 23503 in
// TestDeleteStatus_TicketReferencingIt_ReturnsErrStatusInUse below, the same
// way #278's duplicate-name tests and the pre-existing duplicate-link test
// already trigger a real constraint violation through the shared-transaction
// harness (testutil.TxQueries) — the failing statement is simply the test's
// last one before its transaction is rolled back. See #281.
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

// The real-database half of this coverage — a ticket created holding a
// custom status, then Store.DeleteStatus called directly to force the actual
// tickets_status_id_fkey violation — lives in
// TestDeleteStatus_TicketReferencingIt_ReturnsErrStatusInUse
// (status_violations_integration_test.go). It cannot live in this file: this
// file is `package ticketstore` (so the table above can reach the unexported
// isStatusInUseViolation directly), and internal/testutil imports ticketstore
// itself to build its JoiningTxRunner — importing testutil from here would be
// an import cycle. The integration test therefore lives in the external
// `ticketstore_test` package instead, alongside this one in the same
// directory.
