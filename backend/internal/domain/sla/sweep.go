package sla

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// sweepOutcome is what one ticket's transaction did.
type sweepOutcome struct {
	stamped, announced bool
	stale              int
}

// sweepTicket decides the crossings of t's current record from a reading, then
// writes and announces them in one transaction (#670). The decision is made
// before the transaction, from Record.Elapsed; the statement inside it says
// which of the columns it was asked for it really set, and only those are
// announced.
func (s *Service) sweepTicket(ctx context.Context, t ticket.Ticket, now time.Time, announce bool) (sweepOutcome, error) {
	record, err := s.store.GetRecord(ctx, t.ID)
	if errors.Is(err, ErrNoRecord) {
		return sweepOutcome{}, nil // this ticket is not under an SLA
	}
	if err != nil {
		return sweepOutcome{}, fmt.Errorf("getting SLA record: %w", err)
	}
	policy, err := s.store.GetPolicy(ctx, record.PolicyID)
	if err != nil {
		return sweepOutcome{}, fmt.Errorf("getting SLA policy: %w", err)
	}

	c := Crossings(record, policy, t, now)
	req := ticket.SLAStampRequest{
		At:             now,
		ResponseBreach: c.Breach[0].Due, ResolutionBreach: c.Breach[1].Due,
		ResponseAtRisk: c.AtRisk[0].Due, ResolutionAtRisk: c.AtRisk[1].Due,
	}
	if !req.ResponseBreach && !req.ResolutionBreach && !req.ResponseAtRisk && !req.ResolutionAtRisk {
		return sweepOutcome{}, nil
	}
	if s.atomic == nil {
		return sweepOutcome{}, errors.New("SLA sweep has no transaction runner")
	}

	var out sweepOutcome
	err = s.atomic.InTx(ctx, func(st ticket.Store, _ audit.Store, ch *notification.Changes) error {
		out = sweepOutcome{} // the transaction may run again after a rollback
		got, err := st.StampSLACrossings(ctx, record.ID, req)
		if errors.Is(err, ErrRecordSuperseded) {
			// A re-match replaced the record after it was read (#623): the
			// crossing was decided from the old clock and policy and must not
			// land on, or be announced for, the new one. The next pass reads
			// whatever replaced it.
			return nil
		}
		if err != nil {
			return fmt.Errorf("stamping SLA crossings: %w", err)
		}
		newly := Stamped{
			Breach: [2]bool{got.ResponseBreach, got.ResolutionBreach},
			AtRisk: [2]bool{got.ResponseAtRisk, got.ResolutionAtRisk},
		}
		if newly == (Stamped{}) {
			return nil // another writer set every column first: nothing to say
		}
		out.stamped = true
		a := Announce(newly, c)
		out.stale = a.Stale
		if a.Event == "" || !announce {
			return nil
		}
		tk, err := st.GetByID(ctx, t.ID)
		if err != nil {
			return fmt.Errorf("reading ticket for SLA event: %w", err)
		}
		ch.Raise(slaEvent(tk, record, a, now))
		out.announced = true
		return nil
	})
	if err != nil {
		return sweepOutcome{}, err
	}
	return out, nil
}

// slaEvent is the one event a ticket's pass raises: ids and enums only in
// Payload, the same enums typed for mail, no actor.
func slaEvent(tk ticket.Ticket, rec Record, a Announcement, now time.Time) notification.Event {
	ev := ticket.SystemEvent(tk, now)
	ev.Type = notification.EventType(a.Event)
	ev.Payload = map[string]any{
		"targets":   targetNames(a.Targets),
		"policy_id": rec.PolicyID,
		"record_id": rec.ID,
	}
	alert := &notification.SLAAlert{}
	if a.Event == EventBreached {
		alert.Breached = targetEnums(a.Targets)
		alert.AtRisk = targetEnums(a.AtRiskTargets)
		if len(a.AtRiskTargets) > 0 {
			ev.Payload["at_risk_targets"] = targetNames(a.AtRiskTargets)
		}
	} else {
		alert.AtRisk = targetEnums(a.Targets)
	}
	ev.SLA = alert
	return ev
}

func targetNames(ts []Target) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = string(t)
	}
	return out
}

func targetEnums(ts []Target) []notification.SLATarget {
	if len(ts) == 0 {
		return nil
	}
	out := make([]notification.SLATarget, len(ts))
	for i, t := range ts {
		out[i] = notification.SLATarget(t)
	}
	return out
}
