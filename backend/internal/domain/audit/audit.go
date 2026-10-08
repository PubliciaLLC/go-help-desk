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
type Store interface {
	Create(ctx context.Context, e Entry) error
	ListByEntity(ctx context.Context, entityType string, entityID uuid.UUID, limit, offset int) ([]Entry, error)

	// Search answers the admin-wide audit view (#129): every field on Filter
	// is optional and narrows the result, newest first, ties broken by id.
	// Returns the matching page and what a pager needs, see Page. The count
	// and the page come from the same predicate, so Total is the count of what
	// the page pages.
	//
	// Search knows about staff ticket scope only through Filter.ScopedTo. With
	// it unset every entity is returned; a caller that must not show every
	// entity has to say so there rather than filter the page afterwards, which
	// leaves the count and the offset describing a different sequence from the
	// one the caller reads.
	Search(ctx context.Context, f Filter, limit, offset int) (Page, error)

	// DeleteOlderThan hard-deletes every entry created before cutoff and
	// reports how many were removed. Used by the retention sweep
	// (admin.Service.AuditRetentionDays), which runs only when an operator
	// has set a retention window — the default keeps everything. There is
	// no soft-delete or archive table: an expired entry is gone.
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

// TotalCap is the most Search will count. A table nobody prunes (retention is
// off by default) only grows, and an exact count over it is a full scan on
// every page view, so past this many matches Search stops counting and says
// so. 10,000 is 200 pages of 50, more than anyone pages through.
const TotalCap = 10000

// Page is one page of a Search and what a pager needs to move on from it.
type Page struct {
	Entries []Entry

	// Total is the number of matching entries, counted only up to TotalCap.
	// It is exact whenever TotalCapped is false.
	Total int

	// TotalCapped is true when more than TotalCap entries match; Total is then
	// TotalCap and means "at least this many".
	TotalCapped bool

	// HasMore reports whether an entry exists after this page. It does not
	// depend on Total, which stops at TotalCap while a pager keeps going.
	HasMore bool
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

	// ScopedTo, when set, restricts the result to ticket entries on tickets
	// that staff member may see under the DESIGN.md staff scope: tickets they
	// reported, are assigned, their groups are assigned, or that fall in a
	// Category/Type their groups cover. It is ticket.CanView's staff branch
	// applied inside the query, and server's parity test keeps the two equal.
	//
	// Anything that is not a ticket entry matches nothing, and neither does an
	// entry whose ticket no longer exists — to the person asking that is the
	// same as a ticket they may not see, so the answer cannot be used to tell
	// the two apart. Callers that apply no scope (an administrator, or staff
	// while scope enforcement is off) leave this nil.
	ScopedTo *uuid.UUID
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
