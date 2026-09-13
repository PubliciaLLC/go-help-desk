package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// loggingSLA reports SLA bookkeeping failures.
//
// The ticket service treats these as non-fatal and discards them: a failure to
// record an SLA timestamp must not fail the resolution it describes. Discarded
// silently, though, a transient database error leaves sla_records.resolved_at
// NULL for good, and the breach evaluator later reports a breach the ticket
// never committed — with nothing anywhere explaining why.
//
// It lives here rather than in the domain because CLAUDE.md puts logging at
// the boundary: domain code returns errors and does not log them. This is the
// boundary, so the wrapper reports what the domain deliberately drops.
type loggingSLA struct {
	inner ticket.SLAService
	log   *slog.Logger
}

func newLoggingSLA(inner ticket.SLAService, log *slog.Logger) ticket.SLAService {
	return &loggingSLA{inner: inner, log: log}
}

func (l *loggingSLA) AttachPolicy(ctx context.Context, t ticket.Ticket) error {
	if err := l.inner.AttachPolicy(ctx, t); err != nil {
		l.log.WarnContext(ctx, "attaching SLA policy failed; the ticket has no SLA record",
			"ticket_id", t.ID, "error", err)
		return err
	}
	return nil
}

func (l *loggingSLA) RecordFirstResponse(ctx context.Context, ticketID uuid.UUID, at time.Time) error {
	if err := l.inner.RecordFirstResponse(ctx, ticketID, at); err != nil {
		l.log.WarnContext(ctx, "recording SLA first response failed; the ticket may report a false breach",
			"ticket_id", ticketID, "error", err)
		return err
	}
	return nil
}

func (l *loggingSLA) RecordResolved(ctx context.Context, ticketID uuid.UUID, at time.Time) error {
	if err := l.inner.RecordResolved(ctx, ticketID, at); err != nil {
		l.log.WarnContext(ctx, "recording SLA resolution failed; the ticket may report a false breach",
			"ticket_id", ticketID, "error", err)
		return err
	}
	return nil
}
