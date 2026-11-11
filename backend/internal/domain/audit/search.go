package audit

import "slices"

// searchSpellings is each shown key, spelled the way the writers write it. A
// free-text search looks up these exact spellings (see SearchableKeys), because
// a direct key lookup in the database is about ten times cheaper than expanding
// every row's keys to compare their normalised form, and that difference is the
// one between a search over a large log that answers and one that times out
// (#629, measured on 2,000,000 entries).
//
// The cost of exact spellings is that a key written in another form (apiKey,
// Scope-Path) is shown by Redact but not searched. That fails safe: the search
// can only find less than the view shows, never more. audit_searchable_test.go
// fails when a key is added to shownKeys without a spelling here, or a spelling
// here does not normalise to a shown key.
var searchSpellings = []string{
	"active", "assignee_group_id", "assignee_user_id", "assignment_id",
	"category_id", "client_name", "closed_reopen_policy", "credential_replaced",
	"enabled", "events", "expires_at",
	"field_name", "field_type", "filter", "follow_up_of", "forced_reopen",
	"host", "id", "internal", "item_id",
	"key_linked", "keys_revoked", "node_deleted", "on_behalf", "option_value", "os_user", "owner_user_id",
	"passkey_id", "passkeys_removed", "payload_format", "position", "previous_sla_record_id", "priority",
	"priority_change_reason", "reply_id", "requester_name_mask", "required_on_new", "retired",
	"scope_id", "scope_path", "scope_type", "scopes", "service_user_id", "sessions_revoked",
	"signature_changed", "signed", "sla_outcome", "sla_policy_id", "sla_record_id", "sort_order", "source",
	"status_id", "template_body", "template_html", "template_name", "template_subject", "totp_cleared",
	"type_id", "url_changed", "url_host", "visible_on_new", "visible_to_guests",
}

// SearchableKeys is every Before/After key a free-text search may look inside,
// by its exact spelling: the keys Redact shows as their value, and no other. A
// key Redact hides, or shows only as a placeholder, is never in it. Searching
// one would turn the search into an oracle on the hidden value: "does any
// ticket's subject contain this?" answers yes or no without ever rendering it
// (#629). Sorted, so the same list goes to the query every time.
func SearchableKeys() []string {
	keys := make([]string, 0, len(searchSpellings))
	for _, k := range searchSpellings {
		// Checked again here, not only in the test: a spelling that stops being
		// shown is dropped rather than searched.
		if shownKeys[normaliseKey(k)] && !isSensitive(k) {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
}
