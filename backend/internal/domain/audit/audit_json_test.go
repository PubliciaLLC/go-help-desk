package audit_test

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
)

// Entry is not served yet — #129 is about serving it. The tags exist so that
// read side is built against the right names rather than discovering them
// afterwards, and this pins them before anything depends on them.
//
// Asserting the exact key set rather than round-tripping: a round trip through
// the same struct passes whatever the names happen to be, and "contains"
// passes when an unexpected key is also present.
func TestEntry_JSONContract(t *testing.T) {
	actor := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	e := audit.Entry{
		ID:         uuid.MustParse("99999999-8888-7777-6666-555555555555"),
		ActorID:    &actor,
		EntityType: "ticket",
		EntityID:   uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"),
		Action:     "status_changed",
		Before:     map[string]any{"status": "open"},
		After:      map[string]any{"status": "resolved"},
		CreatedAt:  time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC),
	}

	var got map[string]any
	b, err := json.Marshal(e)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &got))

	require.ElementsMatch(t,
		[]string{"id", "actor_id", "entity_type", "entity_id", "action", "before", "after", "created_at"},
		slices.Collect(maps.Keys(got)),
		"unexpected key on the wire — a new field needs a json tag, or json:\"-\" if it is server-side only")

	require.Equal(t, "status_changed", got["action"])
	require.Equal(t, map[string]any{"status": "open"}, got["before"])
}

// A system-generated action has no actor, and a create has no "before". None
// of these carry omitempty, so the keys must still be present and explicitly
// null — a consumer that branches on key presence would otherwise read a
// system action as a malformed one.
func TestEntry_JSONContract_SystemActionKeepsNullKeys(t *testing.T) {
	e := audit.Entry{
		ID:         uuid.New(),
		ActorID:    nil,
		EntityType: "ticket",
		EntityID:   uuid.New(),
		Action:     "auto_closed",
		Before:     nil,
		After:      map[string]any{"status": "closed"},
		CreatedAt:  time.Now(),
	}

	b, err := json.Marshal(e)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(b, &got))

	require.Contains(t, got, "actor_id")
	require.Nil(t, got["actor_id"])
	require.Contains(t, got, "before")
	require.Nil(t, got["before"])
}
