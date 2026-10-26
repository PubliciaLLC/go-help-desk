// Package emailtemplate holds the stored overrides of the compiled email
// templates (#158). No row means the compiled default; reverting is deleting
// the row. The text is validated by the notify package, which owns the
// restricted template language; this package is only what is stored.
package emailtemplate

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
)

// Override is one edited template. For the shared header and footer (the
// reserved names _header and _footer) Subject is empty.
type Override struct {
	Name    string
	Subject string
	// Body is the plain-text part, always present.
	Body string
	// HTML is the optional HTML part. Empty means none: the mail is plain text
	// only, exactly as before the HTML part existed. Stored as NULL.
	HTML      string
	UpdatedAt time.Time
	UpdatedBy *uuid.UUID
}

// Reader is what sending needs: the overrides for some names, one lookup, no
// cache, so several replicas agree with nothing to invalidate. A name with no
// row is absent from the result.
type Reader interface {
	Overrides(ctx context.Context, names ...string) (map[string]Override, error)
}

// Store is Reader plus the writes the admin editor makes.
type Store interface {
	Reader
	// List returns every stored override.
	List(ctx context.Context) ([]Override, error)
	// Apply sets the override for name, or removes it when next is nil, and
	// writes the audit entry in the same transaction: an entry committed apart
	// from the change it describes is not an audit trail. entry is given the
	// row as it was (nil if there was none) and returns the entry to write, or
	// nil to write none (reverting what was never overridden changes nothing).
	Apply(ctx context.Context, name string, next *Override, entry func(prev *Override) *audit.Entry) error
}
