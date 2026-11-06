package database_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// A reply and its audit entry are one transaction on real Postgres: the audit
// row is written, then fails, and the reply row written before it must go too.
// The in-memory fake cannot tell the transaction's store from the outer one.
func TestAddReply_AuditFailureRollsTheReplyBackOnRealRows(t *testing.T) {
	errStep := errors.New("injected failure")
	f := newChangePriorityFx(t)
	svc := f.service(t, failingStepAtomic{db: f.db.SQL, step: "audit", err: errStep})

	_, err := svc.AddReply(f.ctx, f.tk.ID, "hello", ticket.TextFormatPlain, false, false, "", f.staff, 7, f.newStatusID(t))
	require.ErrorIs(t, err, errStep)

	replies, err := f.ts.ListReplies(f.ctx, f.tk.ID)
	require.NoError(t, err)
	require.Empty(t, replies, "the reply committed without its audit entry")
	require.Empty(t, f.actions(t))
}

func TestAddReply_WritesOneAuditEntryOnRealRows(t *testing.T) {
	f := newChangePriorityFx(t)
	svc := f.service(t, nil)

	_, err := svc.AddReply(f.ctx, f.tk.ID, "hello", ticket.TextFormatPlain, true, false, "", f.staff, 7, f.newStatusID(t))
	require.NoError(t, err)

	require.Equal(t, map[string]int{ticket.AuditActionNoteAdded: 1}, f.actions(t))
}
