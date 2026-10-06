package audit

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Entry records a single mutation on any entity in the system.
type Entry struct {
	ID         uuid.UUID      `json:"id"`
	ActorID    *uuid.UUID     `json:"actor_id"`    // nil for system-generated actions
	EntityType string         `json:"entity_type"` // "ticket", "user", "group", etc.
	EntityID   uuid.UUID      `json:"entity_id"`
	Action     string         `json:"action"` // "created", "status_changed", "assigned", etc.
	Before     map[string]any `json:"before"` // nil for create actions
	After      map[string]any `json:"after"`  // nil for delete actions
	CreatedAt  time.Time      `json:"created_at"`
}

// Store persists audit entries. Implementations must not return errors for
// individual entry failures — log and continue rather than blocking the caller.
type Store interface {
	Create(ctx context.Context, e Entry) error
	ListByEntity(ctx context.Context, entityType string, entityID uuid.UUID, limit, offset int) ([]Entry, error)

	// Search answers the admin-wide audit view (#129): every field on Filter
	// is optional and narrows the result, newest first. Returns the matching
	// page plus the total count across all pages, so a caller can render
	// "n of m" without a second round trip.
	//
	// Search does not know about staff ticket-scope or reporter visibility —
	// that is an HTTP-layer concern (see internal/server's CanViewTicket) —
	// so a caller that must not show every entity has to filter the result,
	// not rely on this to have done it.
	Search(ctx context.Context, f Filter, limit, offset int) ([]Entry, int, error)

	// List is Search without the count, for callers that read page after
	// page and would otherwise pay a full count per page and discard it.
	List(ctx context.Context, f Filter, limit, offset int) ([]Entry, error)

	// DeleteOlderThan hard-deletes every entry created before cutoff and
	// reports how many were removed. Used by the retention sweep
	// (admin.Service.AuditRetentionDays); there is no soft-delete or archive
	// table, per the "1 year, hard delete" scope this shipped with.
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

// Filter narrows a Search call. The zero value matches everything.
//
// Q is deliberately metadata-only: it matches against the entity type and
// action, never against the Before/After payload. Matching payload content
// would let a search for a redacted field's value still surface a hit
// confirming the value existed — see Redact. It does not match the actor's
// name either: a Store has no join to the users table to offer, and name
// resolution already happens one layer up, in the HTTP handler — the same
// place ListByEntity's callers resolve names today.
type Filter struct {
	EntityType string     // exact match, e.g. "ticket"
	Action     string     // exact match, e.g. "resolved"
	ActorID    *uuid.UUID // exact match; nil means "any actor, including system"
	From       *time.Time // inclusive
	To         *time.Time // inclusive
	Q          string     // case-insensitive substring against entity_type and action
}

// sensitiveFields is redacted out of Before/After wherever a diff is shown to
// anyone — including an admin. This is not "hide it from staff", the rule
// #129 and the per-ticket feed's own staff gate already cover; it is "this
// value must never render in a browser at all", the same class of rule as
// the write-only settings in handler_admin_settings.go's secretSettingKeys.
//
// Nothing writes any of these into a Before/After map today — ticketMap
// (internal/domain/ticket/service.go) only ever carries id, status_id,
// priority and subject, and the two user-entity entries that touch
// credentials (mfa_reset, password_reset_by_admin) carry no payload at all.
// This exists for the shape of the problem #129 named — "if a mutation ever
// touched a sensitive field, the value is in there" — not a value observed
// in this codebase, so it is a denylist of plausible names rather than a
// measured list: extend it before extending what writes into Before/After.
var sensitiveFields = map[string]struct{}{
	"password":      {},
	"password_hash": {},
	"mfa_secret":    {},
	"totp_secret":   {},
	"secret":        {},
	"api_key":       {},
	"client_secret": {},
	"token":         {},
	"hash":          {},
}

const redactedPlaceholder = "[redacted]"

// Redact returns copies of before/after with every key in sensitiveFields
// replaced by a placeholder. nil in, nil out — Entry.Before/After are nil for
// create/delete actions respectively, and that distinction (no value existed)
// is different from "a value existed and is hidden", so Redact preserves it
// rather than allocating an empty map.
func Redact(before, after map[string]any) (map[string]any, map[string]any) {
	return redactMap(before), redactMap(after)
}

func redactMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if _, sensitive := sensitiveFields[k]; sensitive {
			out[k] = redactedPlaceholder
			continue
		}
		out[k] = v
	}
	return out
}
