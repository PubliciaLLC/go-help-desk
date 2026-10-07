package audit

import (
	"context"
	"strings"
	"time"
	"unicode"

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
// Cursor is a position in the newest-first order: an entry's created_at and
// id, which together order every entry exactly.
type Cursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

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

	// ListAfter is List addressed by position rather than offset: the entries
	// that come after `after` in the newest-first order, or from the start
	// when after is nil. For a caller reading batch after batch, where an
	// entry written between two reads would shift an offset and repeat a row.
	ListAfter(ctx context.Context, f Filter, after *Cursor, limit int) ([]Entry, error)

	// DeleteOlderThan hard-deletes every entry created before cutoff and
	// reports how many were removed. Used by the retention sweep
	// (admin.Service.AuditRetentionDays), which runs only when an operator
	// has set a retention window — the default keeps everything. There is
	// no soft-delete or archive table: an expired entry is gone.
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

// sensitiveFragments marks a Before/After key as one whose value must never
// render in a browser — including to an admin. This is not "hide it from
// staff", the rule #129 and the per-ticket feed's own staff gate already
// cover; it is "this value must never render at all", the same class of rule
// as the write-only settings in handler_admin_settings.go's secretSettingKeys.
//
// A key is sensitive when its normalised form (see normaliseKey) contains any
// of these, or ends in "key" or "pass". The suffixes exist because the real
// write-only secrets here are named attachment_reputation_virustotal_key and
// smtp_pass, which no stem reaches; a test feeds every entry of
// secretSettingKeys through Redact so the two lists cannot drift. Substring
// rather than exact, so passwordHash, PASSWORD_HASH and user.password land
// alongside password_hash, and a field nobody listed by name (access_token,
// key_pem) still lands on its stem. The cost is over-redaction — "hash" hides
// a hashtag, "key" hides a monkey — which fails safe: a hidden value is a
// nuisance, a rendered secret is not.
//
// Nothing writes any of these into a Before/After map today — ticketMap
// (internal/domain/ticket/service.go) only ever carries id, status_id,
// priority and subject, and the two user-entity entries that touch
// credentials (mfa_reset, password_reset_by_admin) carry no payload at all.
// This exists for the shape of the problem #129 named — "if a mutation ever
// touched a sensitive field, the value is in there" — not a value observed
// in this codebase, so it is a denylist of plausible stems rather than a
// measured list: extend it before extending what writes into Before/After.
var sensitiveFragments = []string{
	"password",
	"passwd",
	"secret",
	"token",
	"hash",
	"keypem",
	"recoverycode",
	"backupcode",
}

// Matched against the end of the normalised key: api_key, private_key and the
// per-provider *_key settings all end in "key".
var sensitiveSuffixes = []string{"key", "pass"}

// normaliseKey lower-cases k and drops everything that is not a letter or
// digit, so "apiKey", "API_KEY" and "api-key" all become "apikey".
func normaliseKey(k string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(k) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isSensitive(k string) bool {
	n := normaliseKey(k)
	for _, f := range sensitiveFragments {
		if strings.Contains(n, f) {
			return true
		}
	}
	for _, f := range sensitiveSuffixes {
		if strings.HasSuffix(n, f) {
			return true
		}
	}
	return false
}

const redactedPlaceholder = "[redacted]"

// Redact returns copies of before/after with the value of every sensitive key
// (see sensitiveFragments) replaced by a placeholder, at any depth: nested
// maps and slices are walked, and a sensitive key hides its whole value
// whatever shape it has. nil in, nil out — Entry.Before/After are nil for
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
		if isSensitive(k) {
			out[k] = redactedPlaceholder
			continue
		}
		out[k] = redactValue(v)
	}
	return out
}

// redactValue handles the two container shapes encoding/json produces; every
// other value is a leaf.
func redactValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return redactMap(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = redactValue(e)
		}
		return out
	default:
		return v
	}
}
