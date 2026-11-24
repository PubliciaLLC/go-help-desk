package tag

import (
	"time"

	"github.com/google/uuid"
)

// Tag is a label that can be attached to tickets.
// Names are always stored lowercase and are globally unique.
type Tag struct {
	ID        uuid.UUID  `json:"id"`
	Name      string     `json:"name"`
	CreatedAt time.Time  `json:"created_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}
