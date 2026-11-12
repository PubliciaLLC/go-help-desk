package customfield_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
)

// #625 item 8 (D24): a follow-up carries its original's custom field values.
// Staff: every value whose field resolves on the new ticket. A requester: only
// the fields the new-ticket form shows (visible_on_new), so a follow-up never
// carries to a requester's ticket what the form kept from them.

type copyFixture struct {
	*rulesFixture
	shown, hidden, gone, retired, typed customfield.FieldDef
	typeID                              uuid.UUID
	from, to                            uuid.UUID
}

func newCopyFixture(t *testing.T) copyFixture {
	t.Helper()
	f := newRulesFixture(t)
	c := copyFixture{rulesFixture: f, typeID: uuid.New(), from: uuid.New(), to: uuid.New()}
	c.shown = f.field("Serial", customfield.FieldTypeText, nil, customfield.CategoryScope, f.cat, true, false, false)
	c.hidden = f.field("Internal code", customfield.FieldTypeText, nil, customfield.CategoryScope, f.cat, false, false, false)
	c.gone = f.field("Old", customfield.FieldTypeText, nil, customfield.CategoryScope, f.cat, true, false, false)
	c.retired = f.field("Colour", customfield.FieldTypeSelect, []string{"red", "blue"}, customfield.CategoryScope, f.cat, true, false, false)
	c.typed = f.field("Asset tag", customfield.FieldTypeText, nil, customfield.TypeScope, c.typeID, true, false, false)

	ctx := context.Background()
	require.NoError(t, f.svc.SetValues(ctx, c.from, map[uuid.UUID]string{
		c.shown.ID: "SN-1", c.hidden.ID: "P1-secret", c.gone.ID: "legacy", c.retired.ID: "blue", c.typed.ID: "AT-9",
	}))
	// Since filed: one field deactivated, one option retired.
	g := f.store.defs[c.gone.ID]
	g.Active = false
	f.store.defs[g.ID] = g
	r := f.store.defs[c.retired.ID]
	r.Options, r.RetiredOptions = []string{"red"}, []string{"blue"}
	f.store.defs[r.ID] = r
	return c
}

func (c copyFixture) held(id uuid.UUID) (string, bool) {
	v, ok := c.store.values[[2]uuid.UUID{c.to, id}]
	return v, ok
}

func TestCopyValues_StaffCarryEveryFieldThatResolves(t *testing.T) {
	c := newCopyFixture(t)
	_, err := c.svc.CopyValues(context.Background(), c.from, c.to, c.cat, &c.typeID, nil, false)
	require.NoError(t, err)

	v, _ := c.held(c.shown.ID)
	require.Equal(t, "SN-1", v)
	v, _ = c.held(c.hidden.ID)
	require.Equal(t, "P1-secret", v, "a field not on the form is still the ticket's, for staff")
	v, _ = c.held(c.typed.ID)
	require.Equal(t, "AT-9", v, "the type is the follow-up's too")
	_, ok := c.held(c.gone.ID)
	require.False(t, ok, "a deactivated field takes no write, so nothing is copied for it")
	_, ok = c.held(c.retired.ID)
	require.False(t, ok, "nor a retired option, which the field no longer accepts")
}

func TestCopyValues_ARequesterCarriesOnlyWhatTheFormShows(t *testing.T) {
	c := newCopyFixture(t)
	_, err := c.svc.CopyValues(context.Background(), c.from, c.to, c.cat, &c.typeID, nil, true)
	require.NoError(t, err)

	v, ok := c.held(c.shown.ID)
	require.True(t, ok)
	require.Equal(t, "SN-1", v)
	_, ok = c.held(c.hidden.ID)
	require.False(t, ok, "a field the form hides is not carried to a requester's ticket")
	v, _ = c.held(c.typed.ID)
	require.Equal(t, "AT-9", v)
}

// A field the follow-up's classification does not have is left behind: a guest's
// follow-up is filed under the category alone.
func TestCopyValues_OnlyFieldsThatResolveOnTheNewClassification(t *testing.T) {
	c := newCopyFixture(t)
	_, err := c.svc.CopyValues(context.Background(), c.from, c.to, c.cat, nil, nil, true)
	require.NoError(t, err)

	_, ok := c.held(c.typed.ID)
	require.False(t, ok, "the type's field does not resolve without the type")
	_, ok = c.held(c.shown.ID)
	require.True(t, ok)
}

// A value the field no longer accepts is left out and does not stop the rest.
func TestCopyValues_OneRefusedValueDoesNotStopTheOthers(t *testing.T) {
	c := newCopyFixture(t)
	s := c.store.defs[c.shown.ID]
	own := 3
	s.MaxLength = &own // lowered since the original was filed: "SN-1" is four characters
	c.store.defs[s.ID] = s

	warnings, err := c.svc.CopyValues(context.Background(), c.from, c.to, c.cat, &c.typeID, nil, false)
	require.NoError(t, err)
	_, ok := c.held(c.shown.ID)
	require.False(t, ok)
	v, _ := c.held(c.hidden.ID)
	require.Equal(t, "P1-secret", v, "the others were copied")
	require.Contains(t, warnings, customfield.Warning{FieldID: c.shown.ID.String(), Reason: customfield.ReasonTooLong})
}

func TestCopyValues_NothingToCopy(t *testing.T) {
	c := newCopyFixture(t)
	writes := c.store.lockedWrites
	warnings, err := c.svc.CopyValues(context.Background(), uuid.New(), c.to, c.cat, nil, nil, false)
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Equal(t, writes, c.store.lockedWrites, "no write was made")
}

// The original is not touched.
func TestCopyValues_LeavesTheOriginalAlone(t *testing.T) {
	c := newCopyFixture(t)
	before := len(c.store.values)
	_, err := c.svc.CopyValues(context.Background(), c.from, c.to, c.cat, &c.typeID, nil, false)
	require.NoError(t, err)
	v, _ := c.store.values[[2]uuid.UUID{c.from, c.shown.ID}]
	require.Equal(t, "SN-1", v)
	require.Greater(t, len(c.store.values), before)
}

// A lookup that fails is an error for the caller to log: the follow-up is
// already committed, so nothing here can undo it.
type brokenValues struct{ joiningFake }

func (brokenValues) ListValuesForTicket(context.Context, uuid.UUID) ([]customfield.TicketFieldValue, error) {
	return nil, errors.New("database unavailable")
}

func TestCopyValues_AFailedReadIsAnError(t *testing.T) {
	store := brokenValues{joiningFake{newFakeStore()}}
	svc := customfield.NewService(store)
	_, err := svc.CopyValues(context.Background(), uuid.New(), uuid.New(), uuid.New(), nil, nil, false)
	require.Error(t, err)
	require.NotErrorIs(t, err, customfield.ErrValidation)
}
