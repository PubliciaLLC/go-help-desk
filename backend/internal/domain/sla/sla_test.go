package sla_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/sla"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/stretchr/testify/require"
)

// The wire format is a contract with the frontend, and it was wrong for the
// entire life of this type: no struct tags meant Go marshalled the field names
// verbatim — {"ID":…,"CategoryID":…} — while types.ts reads id/category_id. All
// six fields disagreed, so the admin SLA table rendered undefined for every
// one. It went unnoticed because SLA is behind a flag that defaults off.
//
// Asserting the exact JSON rather than round-tripping through the same struct:
// a round trip passes no matter what the names are, which is precisely how this
// stayed invisible.
func TestPolicy_JSONContract(t *testing.T) {
	priority := ticket.PriorityHigh
	categoryID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	p := sla.Policy{
		ID:                  uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		Name:                "Gold",
		Priority:            &priority,
		CategoryID:          &categoryID,
		ResponseTargetMin:   30,
		ResolutionTargetMin: 240,
	}

	b, err := json.Marshal(p)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(b, &got))

	require.Equal(t, map[string]any{
		"id":                    "22222222-2222-2222-2222-222222222222",
		"name":                  "Gold",
		"priority":              "high",
		"category_id":           "11111111-1111-1111-1111-111111111111",
		"response_target_min":   float64(30),
		"resolution_target_min": float64(240),
	}, got, "the JSON the frontend reads must not drift from these names")
}

// A catch-all policy has neither priority nor category. Both are omitempty, so
// the keys are absent rather than null — which is what lets the UI show "Any
// priority" instead of an empty badge.
func TestPolicy_JSONOmitsAnyPriorityAndCategory(t *testing.T) {
	b, err := json.Marshal(sla.Policy{Name: "Catch-all", ResponseTargetMin: 60, ResolutionTargetMin: 480})
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(b, &got))

	require.NotContains(t, got, "priority", "a catch-all names no priority")
	require.NotContains(t, got, "category_id")
	require.Equal(t, "Catch-all", got["name"])
}

func TestRecord_JSONContract(t *testing.T) {
	at := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	b, err := json.Marshal(sla.Record{
		TicketID:        uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		PolicyID:        uuid.MustParse("44444444-4444-4444-4444-444444444444"),
		FirstResponseAt: &at,
	})
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(b, &got))

	require.Equal(t, "33333333-3333-3333-3333-333333333333", got["ticket_id"])
	require.Equal(t, "44444444-4444-4444-4444-444444444444", got["policy_id"])
	require.Contains(t, got, "first_response_at")
	require.NotContains(t, got, "resolved_at", "unset timestamps are absent, not null")
}
