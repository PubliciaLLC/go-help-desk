package catalogue_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/catalogue"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

type settings struct {
	submission, guestTypes, guestItems, userItems bool
}

func (s settings) GuestSubmissionEnabled(context.Context) bool { return s.submission }
func (s settings) GuestsShowTypes(context.Context) bool        { return s.guestTypes }
func (s settings) GuestsShowItems(context.Context) bool        { return s.guestItems }
func (s settings) UsersShowItems(context.Context) bool         { return s.userItems }

// The one policy: what each kind of caller sees of the tree, for every
// combination of the four inputs.
func TestVisibleLevels(t *testing.T) {
	all := catalogue.Levels{Types: true, Items: true}
	for _, sub := range []bool{false, true} {
		for _, gt := range []bool{false, true} {
			for _, gi := range []bool{false, true} {
				for _, ui := range []bool{false, true} {
					s := settings{sub, gt, gi, ui}
					name := func(w string) string {
						return w + map[bool]string{true: " sub", false: ""}[sub] + map[bool]string{true: " gt", false: ""}[gt] +
							map[bool]string{true: " gi", false: ""}[gi] + map[bool]string{true: " ui", false: ""}[ui]
					}
					t.Run(name("staff"), func(t *testing.T) {
						require.Equal(t, all, catalogue.VisibleLevels(context.Background(), s, catalogue.Staff))
					})
					t.Run(name("requester"), func(t *testing.T) {
						require.Equal(t, catalogue.Levels{Types: true, Items: ui},
							catalogue.VisibleLevels(context.Background(), s, catalogue.Requester))
					})
					t.Run(name("guest"), func(t *testing.T) {
						want := catalogue.Levels{Types: sub && gt, Items: sub && gt && gi}
						require.Equal(t, want, catalogue.VisibleLevels(context.Background(), s, catalogue.Guest))
					})
				}
			}
		}
	}
}

// A caller that forgets to say who is asking sees nothing at all: the zero value
// of Who is Unknown, which sees no more than any real kind could (in every
// combination of the settings), and so does a value that is not a kind.
func TestVisibleLevels_FailsClosed(t *testing.T) {
	var zero catalogue.Who
	require.Equal(t, catalogue.Unknown, zero)
	for _, sub := range []bool{false, true} {
		for _, flags := range []bool{false, true} {
			s := settings{sub, flags, flags, flags}
			ctx := context.Background()
			require.Equal(t, catalogue.Levels{}, catalogue.VisibleLevels(ctx, s, zero))
			require.Equal(t, catalogue.Levels{}, catalogue.VisibleLevels(ctx, s, catalogue.Who(99)))
			unknown := catalogue.VisibleLevels(ctx, s, zero)
			for _, who := range []catalogue.Who{catalogue.Guest, catalogue.Requester, catalogue.Staff} {
				got := catalogue.VisibleLevels(ctx, s, who)
				require.False(t, unknown.Types && !got.Types, "Unknown must not see a level a real kind does not")
				require.False(t, unknown.Items && !got.Items, "Unknown must not see a level a real kind does not")
			}
		}
	}
}

func TestWhoOfRole(t *testing.T) {
	require.Equal(t, catalogue.Staff, catalogue.WhoOfRole(user.RoleAdmin))
	require.Equal(t, catalogue.Staff, catalogue.WhoOfRole(user.RoleStaff))
	require.Equal(t, catalogue.Requester, catalogue.WhoOfRole(user.RoleUser))
	require.Equal(t, catalogue.Requester, catalogue.WhoOfRole(user.Role("")), "a role nobody recognises is a requester")
	require.Equal(t, catalogue.Requester, catalogue.WhoOfRole(user.Role("superuser")))
}
