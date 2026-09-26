package slastore

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/categorystore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/category"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// TestIsPolicyInUseViolation pins #261's race backstop: the table below only
// proves the hand-typed constraint name matches a copy of itself, so a typo
// present in both places would still pass every case. The real-database half
// of this coverage — a policy with a referencing sla_records row, and
// s.q.DeleteSLAPolicy called directly to force the actual
// sla_records_policy_id_fkey violation — is
// TestDeletePolicy_RecordReferencingIt_TriggersRealForeignKeyViolation below.
// Both live in this file, in `package slastore` rather than `slastore_test`,
// because the table needs the unexported isPolicyInUseViolation directly and
// (unlike the analogous ticketstore case, #281) internal/testutil does not
// import slastore, so there is no import cycle to split across files for.
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

// TestDeletePolicy_RecordReferencingIt_TriggersRealForeignKeyViolation
// exercises isPolicyInUseViolation's constraint-name matching against a REAL
// Postgres 23503, closing the gap TestIsPolicyInUseViolation's own table
// cannot: that table only proves the hand-typed constraint name
// "sla_records_policy_id_fkey" matches a copy of itself, so a typo present in
// both places would still pass every case. Here a policy is created with a
// referencing sla_records row, and s.q.DeleteSLAPolicy is called directly —
// bypassing Store.DeletePolicy's CountSLARecordsByPolicy check entirely,
// which is what normally keeps an ordinary admin request from ever reaching
// the database with a policy still in use — to force the actual
// sla_records_policy_id_fkey violation and confirm isPolicyInUseViolation
// recognizes it. Mirrors ticketstore's
// TestDeleteStatus_TicketReferencingIt_ReturnsErrStatusInUse (#281), which
// did the same for the analogous status case. See #286.
func TestDeletePolicy_RecordReferencingIt_TriggersRealForeignKeyViolation(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()

	us := userstore.New(q)
	cs := categorystore.New(q)
	ts := ticketstore.New(q)
	s := New(q)

	reporter := user.User{
		ID:          uuid.New(),
		Email:       "policy-violation-" + uuid.NewString() + "@test.local",
		DisplayName: "Reporter",
		Role:        user.RoleUser,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	require.NoError(t, us.Create(ctx, reporter))

	cat := category.Category{ID: uuid.New(), Name: "Policy violation " + uuid.NewString()[:8], SortOrder: 1, Active: true}
	require.NoError(t, cs.CreateCategory(ctx, cat))

	newSt, err := ts.GetStatusByName(ctx, ticket.StatusNameNew)
	require.NoError(t, err)

	tk := ticket.Ticket{
		ID:             uuid.New(),
		TrackingNumber: ticket.TrackingNumber("PV-" + uuid.NewString()[:8]),
		Subject:        "policy violation test",
		Description:    "policy violation test",
		CategoryID:     cat.ID,
		Priority:       ticket.PriorityMedium,
		StatusID:       newSt.ID,
		ReporterUserID: &reporter.ID,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	require.NoError(t, ts.Create(ctx, tk))

	policy := sla.Policy{
		ID:                  uuid.New(),
		Name:                "Policy violation test policy " + uuid.NewString()[:8],
		ResponseTargetMin:   30,
		ResolutionTargetMin: 60,
	}
	require.NoError(t, s.CreatePolicy(ctx, policy))
	require.NoError(t, s.CreateRecord(ctx, sla.Record{TicketID: tk.ID, PolicyID: policy.ID}))

	// Bypasses Store.DeletePolicy's count-first check entirely: the record
	// above still references this policy, so this hits the real
	// sla_records_policy_id_fkey violation rather than the count-based
	// refusal an admin request would see first.
	delErr := s.q.DeleteSLAPolicy(ctx, policy.ID)
	require.Error(t, delErr)
	require.True(t, isPolicyInUseViolation(delErr), "got %v, want a real sla_records_policy_id_fkey violation", delErr)
}
