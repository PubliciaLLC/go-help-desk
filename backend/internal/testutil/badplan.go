package testutil

import (
	"database/sql"
	"testing"
)

// ForceRescannedBatchPlan makes the planner prefer, for the rest of tx, the
// plan that exposes a batch statement of the shape
//
//	DELETE FROM t WHERE id IN (SELECT id ... LIMIT n FOR UPDATE SKIP LOCKED)
//
// as unsafe: a nested-loop semi join that re-runs the LIMIT subquery once per
// row of the outer table. Each re-run picks rows again, so one statement
// removes (or claims) more than n. Which plan Postgres picks otherwise follows
// the table statistics, so a test that only checks the row count passes on one
// database and fails on another. The settings steer away from every alternative
// that would run the subquery once: hash and merge joins, a materialized
// inner side, and de-duplicating the subquery's output.
//
// SET LOCAL, so the settings end with the transaction.
func ForceRescannedBatchPlan(t testing.TB, tx *sql.Tx) {
	t.Helper()
	for _, setting := range []string{
		"enable_hashjoin", "enable_mergejoin", "enable_material",
		"enable_memoize", "enable_hashagg", "enable_sort",
	} {
		if _, err := tx.Exec("SET LOCAL " + setting + " = off"); err != nil {
			t.Fatalf("forcing the plan (%s): %v", setting, err)
		}
	}
}
