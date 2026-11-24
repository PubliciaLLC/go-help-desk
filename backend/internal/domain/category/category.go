package category

import "github.com/google/uuid"

// Category is the top level of the CTI classification hierarchy.
type Category struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	SortOrder int       `json:"sort_order"`
	Active    bool      `json:"active"`
}

// Type is the second level of the CTI hierarchy, scoped to a Category.
// Types are optional — a Category may have no Types.
type Type struct {
	ID         uuid.UUID `json:"id"`
	CategoryID uuid.UUID `json:"category_id"`
	Name       string    `json:"name"`
	SortOrder  int       `json:"sort_order"`
	Active     bool      `json:"active"`
}

// Item is the third and deepest level of the CTI hierarchy, scoped to a Type.
// Items are optional — a Type may have no Items.
type Item struct {
	ID        uuid.UUID `json:"id"`
	TypeID    uuid.UUID `json:"type_id"`
	Name      string    `json:"name"`
	SortOrder int       `json:"sort_order"`
	Active    bool      `json:"active"`
}

// CTI is a resolved classification triple. Type and Item are optional downward:
// a ticket may have only a Category, a Category+Type, or all three.
type CTI struct {
	Category Category
	Type     *Type
	Item     *Item
}
