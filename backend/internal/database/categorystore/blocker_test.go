package categorystore

import (
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

// The race fallback names what blocked the delete by constraint name, so an
// SLA record is not reported as a ticket.
func TestBlocker_NamesTheConstraint(t *testing.T) {
	for constraint, want := range map[string]string{
		"sla_records_policy_id_fkey": "an SLA record",
		"tickets_category_id_fkey":   "a ticket",
		"tickets_type_id_fkey":       "a ticket",
		"tickets_item_id_fkey":       "a ticket",
		"something_else_fkey":        "another record",
	} {
		t.Run(constraint, func(t *testing.T) {
			err := fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: "23503", ConstraintName: constraint})
			require.Equal(t, want, blocker(err))
		})
	}
}
