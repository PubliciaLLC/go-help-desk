package auth_test

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

// handleListOAuthClients serves []OAuthClient straight to the client. Untagged,
// that put HashedSecret on the wire alongside Go field names. APIKey next door
// has carried `json:"-"` on its hash since it was written; this struct is the
// one that was missed.
//
// Asserting exact keys rather than round-tripping: a round trip through the
// same struct passes whatever the names happen to be.
func TestOAuthClient_JSONContract(t *testing.T) {
	c := auth.OAuthClient{
		ID:           uuid.MustParse("11111111-2222-3333-4444-555555555555"),
		ClientID:     "abc123",
		HashedSecret: "SENTINEL_HASHED_SECRET",
		Name:         "Billing sync",
		Scopes:       []string{"tickets:read"},
		CreatedAt:    time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC),
	}

	b, err := json.Marshal(c)
	require.NoError(t, err)

	// The value itself, not just the key: a rename still has to not ship it.
	require.NotContains(t, string(b), "SENTINEL_HASHED_SECRET",
		"the secret hash must never reach a client")

	var got map[string]any
	require.NoError(t, json.Unmarshal(b, &got))

	require.NotContains(t, got, "hashed_secret")
	require.NotContains(t, got, "HashedSecret")

	// The exact set, not just "contains": a future secret-bearing field added
	// without a tag would otherwise ship PascalCase with every test passing.
	// handleCreateOAuthClient hand-builds client_id/name; list must agree.
	require.ElementsMatch(t,
		[]string{"id", "client_id", "name", "scopes", "created_at"},
		slices.Collect(maps.Keys(got)),
		"unexpected key on the wire — a new field needs a json tag, or json:\"-\" if it is a secret")
}
