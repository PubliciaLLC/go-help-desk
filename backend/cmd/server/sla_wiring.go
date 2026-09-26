package main

import (
	"context"
	"log/slog"
	"time"

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

func (l *loggingSLA) RecordFirstResponse(ctx context.Context, t ticket.Ticket, at time.Time) error {
	if err := l.inner.RecordFirstResponse(ctx, t, at); err != nil {
		l.log.WarnContext(ctx, "recording SLA first response failed; the ticket may report a false breach",
			"ticket_id", t.ID, "error", err)
		return err
	}
	return nil
}

func (l *loggingSLA) RecordResolved(ctx context.Context, t ticket.Ticket, at time.Time) error {
	if err := l.inner.RecordResolved(ctx, t, at); err != nil {
		l.log.WarnContext(ctx, "recording SLA resolution failed; the ticket may report a false breach",
			"ticket_id", t.ID, "error", err)
		return err
	}
	return nil
}

// slaEnabler is the one method gatedSLA needs from admin.Service. Declared
// narrowly here, rather than importing admin.Service's whole surface into
// this tiny gate, and satisfied by *admin.Service without either package
// naming the other.
type slaEnabler interface {
	SLAEnabled(ctx context.Context) (bool, error)
}

// gatedSLA reads the SLA feature toggle LIVE, from the same admin-settings
// row the ticket-view indicator and the admin UI already read (see
// admin.Service.SLAEnabled and internal/server/ticket_view.go), instead of a
// value decided once at process boot from the SLA_ENABLED env var.
//
// Before this wrapper existed, ticket.Service's whole SLA wiring was either
// nil or non-nil for the life of the process, chosen once from cfg.SLAEnabled
// in run(). The admin Settings-page toggle is a separate, DB-backed setting
// that DESIGN.md documents as THE feature switch — SLA_ENABLED is described
// there only as a way to pre-enable it at startup — so an admin turning that
// toggle on attached no SLA records, ran no breach sweep, and showed no
// indicator until the process was restarted with SLA_ENABLED=true. The
// default deployment path (env var unset, admin enables via the UI) left the
// feature completely and silently dead. gatedSLA is what main.go now always
// wires in, so every call — AttachPolicy on ticket creation,
// RecordFirstResponse/RecordResolved on reply/resolve — checks the DB setting
// at the moment it runs and the feature turns on and off with the toggle
// alone. The breach sweep goroutine in run() reads the same setting on its
// own each tick, for the same reason.
type gatedSLA struct {
	inner ticket.SLAService
	admin slaEnabler
	log   *slog.Logger
}

func newGatedSLA(inner ticket.SLAService, admin slaEnabler, log *slog.Logger) ticket.SLAService {
	if log == nil {
		log = slog.Default()
	}
	return &gatedSLA{inner: inner, admin: admin, log: log}
}

func (g *gatedSLA) AttachPolicy(ctx context.Context, t ticket.Ticket) error {
	enabled, err := g.admin.SLAEnabled(ctx)
	if err != nil {
		// Fail safe rather than silently: a transient read failure must not
		// attach a policy nobody confirmed is wanted, but it also must not
		// vanish the way `v, _ := ...` did before #216 — logged here, at the
		// boundary, since CLAUDE.md keeps domain code (admin.Service) from
		// logging it itself.
		g.log.WarnContext(ctx, "reading SLA enabled setting failed; treating SLA as disabled for this ticket",
			"ticket_id", t.ID, "error", err)
		return nil
	}
	if !enabled {
		return nil
	}
	return g.inner.AttachPolicy(ctx, t)
}

// RecordFirstResponse and RecordResolved are NOT gated on the toggle (#216).
// Unlike AttachPolicy (which must not create a new record while the feature
// is off) and the sweep (which must not stamp a new breach while it is off),
// a record here is being written onto an sla_records row that only exists
// because the toggle was ON when the ticket was created — writing a fact onto
// an existing record is always safe. Gating these silently DROPPED the fact
// instead of deferring it: if staff replied to or resolved a ticket during an
// off period, first_response_at/resolved_at were never written, and flipping
// the toggle back on left the ticket looking still-unresolved to the next
// sweep tick, which then stamped a false breach that never clears.
func (g *gatedSLA) RecordFirstResponse(ctx context.Context, t ticket.Ticket, at time.Time) error {
	return g.inner.RecordFirstResponse(ctx, t, at)
}

func (g *gatedSLA) RecordResolved(ctx context.Context, t ticket.Ticket, at time.Time) error {
	return g.inner.RecordResolved(ctx, t, at)
}
