package customfield

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Publishing a field to anonymous visitors is recorded, on both sides of the
// change, and an attachment that is not offered to guests records exactly what
// it always did (TestAssignmentEntries).
func TestAssignmentEntries_GuestFlag(t *testing.T) {
	field, scope := uuid.New(), uuid.New()
	a := Assignment{ID: uuid.New(), FieldDefID: field, ScopeType: CategoryScope, ScopeID: scope, VisibleOnNew: true}
	info := AssignmentInfo{FieldName: "Dept", ScopePath: "Hardware"}

	offered := a
	offered.VisibleToGuests = true
	require.Equal(t, true, entryAttached(nil, info, offered).After["visible_to_guests"])
	require.NotContains(t, entryAttached(nil, info, a).After, "visible_to_guests")
	require.Equal(t, true, entryDetached(nil, info, offered).Before["visible_to_guests"])

	changed := entriesForAttachmentChange(nil, info, a, offered)
	require.Len(t, changed, 1, "the flag alone is a change")
	require.Equal(t, false, changed[0].Before["visible_to_guests"])
	require.Equal(t, true, changed[0].After["visible_to_guests"])

	withdrawn := entriesForAttachmentChange(nil, info, offered, a)
	require.Len(t, withdrawn, 1)
	require.Equal(t, true, withdrawn[0].Before["visible_to_guests"])
	require.Equal(t, false, withdrawn[0].After["visible_to_guests"])
}
