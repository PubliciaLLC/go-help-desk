package slastore

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// TestCreatePolicy_UnknownCategory_ReturnsErrUnknownCategory pins #276:
// sla_policies.category_id REFERENCES categories(id), and creating a policy
// against a category_id that names no row used to reach the client as raw
// Postgres text under a 400 rather than sla.ErrUnknownCategory. This also
// confirms the guessed constraint name, sla_policies_category_id_fkey.
func TestCreatePolicy_UnknownCategory_ReturnsErrUnknownCategory(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()

	s := New(q)
	unknownCategory := uuid.New()

	err := s.CreatePolicy(ctx, sla.Policy{
		ID:                  uuid.New(),
		Name:                "Unknown category policy",
		CategoryID:          &unknownCategory,
		ResponseTargetMin:   60,
		ResolutionTargetMin: 480,
	})
	require.ErrorIs(t, err, sla.ErrUnknownCategory)
}

// TestUpdatePolicy_UnknownCategory_ReturnsErrUnknownCategory is the PATCH
// counterpart: a policy created without a category, then updated to point at
// one that does not exist, must see the same mapped error rather than raw
// Postgres text (#276).
func TestUpdatePolicy_UnknownCategory_ReturnsErrUnknownCategory(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, rollback := testutil.TxQueries(t, db)
	defer rollback()
	ctx := context.Background()

	s := New(q)
	policy := sla.Policy{
		ID:                  uuid.New(),
		Name:                "Valid at first",
		ResponseTargetMin:   60,
		ResolutionTargetMin: 480,
	}
	require.NoError(t, s.CreatePolicy(ctx, policy))

	unknownCategory := uuid.New()
	policy.CategoryID = &unknownCategory

	err := s.UpdatePolicy(ctx, policy)
	require.ErrorIs(t, err, sla.ErrUnknownCategory)
}
