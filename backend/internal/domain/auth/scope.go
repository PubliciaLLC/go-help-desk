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
	ResourcePlugins, ResourceWebhooks, ResourceCredentials,
}

// All returns every valid scope, for validation and for the admin UI's picker.
func All() []Scope {
	out := make([]Scope, 0, len(resources)*2)
	for _, r := range resources {
		out = append(out, Scope{r, ActionRead}, Scope{r, ActionWrite})
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
// hand must not become a wildcard.
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
