package database_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/reputation"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// The fourth provider at the column that refuses everything else (#168).
//
// attachment_reputation.provider is CHECKed, so a provider added to the Go
// constants and forgotten in migration 000024 produces a feature that works in
// every unit test and writes nothing on a real instance: the verdict is
// fetched, the insert fails, and the next render fetches it again.
func TestAttachmentReputation_AcceptsCIRCLAsAProvider(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)

	q, rollback := testutil.TxQueries(t, db)
	defer rollback()

	got, err := q.UpsertAttachmentReputation(context.Background(), dbgen.UpsertAttachmentReputationParams{
		Sha256:   repHashA,
		Provider: reputation.ProviderCIRCL,
		State:    string(reputation.Known),
	})
	require.NoError(t, err)
	require.Equal(t, "circl", got.Provider,
		"the column and the Go constant have to agree, or a cached verdict is unreachable")
}
