// Package catalogue holds the one rule for which levels of the Category > Type >
// Item tree a caller may see (#711). It is deliberately tiny: every surface that
// serves, accepts or returns a type or an item (the REST catalogue, a
// requester's tickets and filters, MCP, the forms) asks VisibleLevels and none
// restates the rule, so a surface that forgets cannot be told apart from a
// surface that is wrong.
package catalogue

import (
	"context"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Who is the kind of caller asking. The zero value is Unknown, which sees
// nothing below the category, so a caller that forgets to say who is asking (a
// nil actor, a value nobody set) gets the least and not a guest's levels.
type Who int

const (
	Unknown   Who = iota // not said: sees nothing below the category
	Guest                // no account: the public guest form
	Requester            // a registered user with the user role
	Staff                // staff and administrators
)

// WhoOfRole maps an account's role to the caller kind. A machine credential
// acts with its owner's role, so an API key or OAuth client is whoever it stands
// for; a role nobody recognises is a requester.
func WhoOfRole(role user.Role) Who {
	if role == user.RoleAdmin || role == user.RoleStaff {
		return Staff
	}
	return Requester
}

// Levels is which levels below the category a caller sees. The category is
// always visible.
type Levels struct {
	Types bool
	Items bool
}

// Settings is what the rule reads. admin.Service satisfies it. The two guest
// readers are the stored values: VisibleLevels does the combining.
type Settings interface {
	GuestSubmissionEnabled(ctx context.Context) bool
	GuestsShowTypes(ctx context.Context) bool
	GuestsShowItems(ctx context.Context) bool
	UsersShowItems(ctx context.Context) bool
}

// VisibleLevels is the rule.
//
//	Staff     every level.
//	Requester Types always, Items while users_show_items is on.
//	Guest     Types while guest submission is on and guests_show_types is;
//	          Items as well while guests_show_items is (an item hangs under a
//	          type, so Items on with Types off is Items off).
//
// Unknown, and any other value of who, sees nothing below the category.
func VisibleLevels(ctx context.Context, s Settings, who Who) Levels {
	switch who {
	case Staff:
		return Levels{Types: true, Items: true}
	case Requester:
		return Levels{Types: true, Items: s.UsersShowItems(ctx)}
	case Guest:
		if !s.GuestSubmissionEnabled(ctx) || !s.GuestsShowTypes(ctx) {
			return Levels{}
		}
		return Levels{Types: true, Items: s.GuestsShowItems(ctx)}
	}
	return Levels{}
}
