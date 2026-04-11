package category

import "github.com/google/uuid"

// Category is the top level of the CTI classification hierarchy.
type Category struct {
	ID        uuid.UUID
	Name      string
	SortOrder int
	Active    bool
}

// Type is the second level of the CTI hierarchy, scoped to a Category.
// Types are optional — a Category may have no Types.
type Type struct {
	ID         uuid.UUID
	CategoryID uuid.UUID
	Name       string
	SortOrder  int
	Active     bool
}

// Item is the third and deepest level of the CTI hierarchy, scoped to a Type.
// Items are optional — a Type may have no Items.
type Item struct {
	ID        uuid.UUID
	TypeID    uuid.UUID
	Name      string
	SortOrder int
	Active    bool
}

// CTI is a resolved classification triple. Type and Item are optional downward:
// a ticket may have only a Category, a Category+Type, or all three.
type CTI struct {
	Category Category
	Type     *Type
	Item     *Item
}
