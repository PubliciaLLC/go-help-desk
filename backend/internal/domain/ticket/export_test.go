package ticket

import (
	"context"
	"time"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
)

// ChangePriorityInTx exposes changePriorityInTx to the external test package.
// It is unexported because its only production callers are ChangePriority and
// the reopen paths (finishReopenInTx), all inside this package; a test has to run
// it inside a caller's transaction the way a reopen does.
func (s *Service) ChangePriorityInTx(ctx context.Context, st Store, au audit.Store, t Ticket,
	p Priority, reason PriorityChangeReason, actor Actor, now time.Time) (Ticket, error) {
	return s.changePriorityInTx(ctx, st, au, t, p, reason, actor, now)
}
