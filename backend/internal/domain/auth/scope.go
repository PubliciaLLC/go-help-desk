package auth

import (
	"fmt"
	"strings"
)

// Scopes narrow what a credential may do. They never widen it.
//
// A scope is checked AFTER the role, so a credential cannot reach anything its
// owner could not reach anyway: an API key acts at its owner's role, and an
// OAuth client acts as staff. Granting `users:write` to a key owned by a
// reporting user grants nothing.
//
// An empty scope list denies everything. That is deliberate and it is a
// breaking change: credentials issued before enforcement existed carry no
// scopes, and they stop working until they are re-issued. The alternative —
// treating "no scopes" as "all scopes" — would mean the only credentials the
// enforcement protects are the ones that opted in, which is the state this
// replaces.
type Action string

const (
	ActionRead  Action = "read"
	ActionWrite Action = "write"
)

// Resources are the route groups a credential can reach. They are deliberately
// coarser than the routes themselves: `categories` covers types, items and
// field assignments, because those are all one administrative concern and an
// integration that may edit a category has no reason to be refused its types.
const (
	ResourceTickets         = "tickets"
	ResourceUsers           = "users"
	ResourceGroups          = "groups"
	ResourceCategories      = "categories"
	ResourceTags            = "tags"
	ResourceCannedResponses = "canned_responses"
	ResourceSLA             = "sla"
	ResourceSettings        = "settings"
	ResourcePlugins         = "plugins"
	ResourceWebhooks        = "webhooks"

	// ResourceCredentials covers API keys and OAuth clients. A credential
	// holding credentials:write can mint another credential, so it is an
	// escalation path by design — treat it as equivalent to the owner's full
	// access, and prefer not to grant it to an integration.
	ResourceCredentials = "credentials"

	// ResourceAudit covers the admin-wide audit view (#362). A signed-in
	// session needs no scope; a machine credential needs audit:read, which
	// an administrator grants on purpose. tickets:read used to reach it, so
	// an integration key could read every entity's audit entries without
	// anything about the key saying so. There is no audit:write: only the
	// running server writes audit entries, so ParseScope refuses it when a
	// credential is created, and one already stored grants nothing — not
	// even audit:read through write-implies-read (#371).
	ResourceAudit = "audit"
)

// Scope is one resource and one action.
type Scope struct {
	Resource string
	Action   Action
}

func (s Scope) String() string { return s.Resource + ":" + string(s.Action) }

var resources = []string{
	ResourceTickets, ResourceUsers, ResourceGroups, ResourceCategories,
	ResourceTags, ResourceCannedResponses, ResourceSLA, ResourceSettings,
	ResourcePlugins, ResourceWebhooks, ResourceCredentials, ResourceAudit,
}

// writable reports whether a resource has a write scope. The audit log has
// none: no credential may write it (#371).
func writable(resource string) bool { return resource != ResourceAudit }

// All returns every valid scope, for validation and for the admin UI's picker.
func All() []Scope {
	out := make([]Scope, 0, len(resources)*2)
	for _, r := range resources {
		out = append(out, Scope{r, ActionRead})
		if writable(r) {
			out = append(out, Scope{r, ActionWrite})
		}
	}
	return out
}

// ParseScope reads "resource:action" and rejects anything else. Unknown scopes
// are an error rather than a silent no-op: a typo in an integration's config
// should fail loudly at credential creation, not quietly grant nothing and be
// discovered in production.
func ParseScope(s string) (Scope, error) {
	resource, action, ok := strings.Cut(s, ":")
	if !ok {
		return Scope{}, fmt.Errorf("scope %q: want resource:action", s)
	}
	if action != string(ActionRead) && action != string(ActionWrite) {
		return Scope{}, fmt.Errorf("scope %q: action must be read or write", s)
	}
	for _, r := range resources {
		if r == resource {
			if Action(action) == ActionWrite && !writable(resource) {
				return Scope{}, fmt.Errorf("scope %q: %s is read-only; only the server writes it", s, resource)
			}
			return Scope{resource, Action(action)}, nil
		}
	}
	return Scope{}, fmt.Errorf("scope %q: unknown resource %q", s, resource)
}

// ValidateScopes returns an error naming the first invalid entry.
func ValidateScopes(scopes []string) error {
	for _, s := range scopes {
		if _, err := ParseScope(s); err != nil {
			return err
		}
	}
	return nil
}

// Allows reports whether the granted scopes permit the required one.
//
// Write implies read on the same resource. An integration that may create
// tickets but may not read them back is not a shape anyone wants, and making
// callers grant both invites granting write-only by mistake.
//
// Malformed granted scopes are ignored rather than trusted. They cannot be
// created through the API — ValidateScopes rejects them — but a row edited by
// hand must not become a wildcard. audit:write stored before #371 refused it
// is ignored the same way, so it does not imply audit:read.
// There is no wildcard. A credential that should reach everything lists every
// scope it needs, so what it can do is legible from the credential itself, and
// a resource added later does not silently widen credentials that already
// exist. "*", "tickets:*" and the like are not scopes and grant nothing.
func Allows(granted []string, required Scope) bool {
	for _, g := range granted {
		s, err := ParseScope(g)
		if err != nil {
			continue
		}
		if s.Resource != required.Resource {
			continue
		}
		if s.Action == required.Action || s.Action == ActionWrite {
			return true
		}
	}
	return false
}

// Subset reports whether every scope in want is covered by have.
//
// A credential that can create credentials could otherwise mint one carrying
// scopes it does not hold itself, which makes credentials:write equivalent to
// every scope: hold only that, mint a key with users:write and settings:write,
// and use it. Escalation by one extra request is not a boundary.
//
// Callers apply this only to machine credentials. A signed-in administrator
// issuing a credential is not escalating — they already hold everything the
// credential could be granted.
func Subset(have, want []string) (string, bool) {
	for _, w := range want {
		s, err := ParseScope(w)
		if err != nil {
			return w, false
		}
		if !Allows(have, s) {
			return w, false
		}
	}
	return "", true
}
