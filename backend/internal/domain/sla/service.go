package sla

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// Service evaluates SLA policies against tickets.
type Service struct{ store Store }

// NewService returns a Service backed by the given Store.
func NewService(store Store) *Service { return &Service{store: store} }

// AttachPolicy finds the best matching SLA policy for a ticket and creates an
// SLA record for it. Called when a new ticket is created.
func (s *Service) AttachPolicy(ctx context.Context, t ticket.Ticket) error {
	policy, err := s.store.FindPolicy(ctx, t.Priority, t.CategoryID)
	if err != nil {
		return fmt.Errorf("finding SLA policy: %w", err)
	}
	if policy == nil {
		return nil // no policy configured for this priority/category
	}
	return s.store.CreateRecord(ctx, Record{
		TicketID: t.ID,
		PolicyID: policy.ID,
	})
}

// RecordFirstResponse marks the time of the first staff reply on a ticket.
// It is a no-op when already recorded.
func (s *Service) RecordFirstResponse(ctx context.Context, ticketID uuid.UUID, at time.Time) error {
	record, err := s.store.GetRecord(ctx, ticketID)
	if errors.Is(err, ErrNoRecord) {
		return nil // this ticket is not under an SLA
	}
	if err != nil {
		// Previously every error was treated as "no record". A transient
		// database failure therefore lost the first-response timestamp
		// permanently, and the ticket later looked like a genuine breach.
		return fmt.Errorf("getting SLA record: %w", err)
	}
	if record.FirstResponseAt != nil {
		return nil // already recorded
	}
	record.FirstResponseAt = &at
	return s.store.UpdateRecord(ctx, record)
}

// RecordResolved stamps when a ticket was resolved, so resolution breaches are
// judged against the time it was actually resolved.
//
// Nothing wrote this field. IsResolutionBreached therefore saw a NULL
// ResolvedAt on every ticket and would have reported each one as breached the
// moment its deadline passed, however promptly it had been resolved.
func (s *Service) RecordResolved(ctx context.Context, ticketID uuid.UUID, at time.Time) error {
	record, err := s.store.GetRecord(ctx, ticketID)
	if errors.Is(err, ErrNoRecord) {
		return nil // this ticket is not under an SLA
	}
	if err != nil {
		return fmt.Errorf("getting SLA record: %w", err)
	}
	if record.ResolvedAt != nil {
		return nil // already recorded
	}
	record.ResolvedAt = &at
	return s.store.UpdateRecord(ctx, record)
}

// EvaluateBreaches checks whether a ticket has breached its SLA targets and
// stamps the breach timestamps if so. Called per ticket, from the periodic
// sweep (see SweepBreaches) or directly, as service_test.go still does.
func (s *Service) EvaluateBreaches(ctx context.Context, t ticket.Ticket, now time.Time) error {
	_, err := s.evaluate(ctx, t, now)
	return err
}

// evaluate is EvaluateBreaches' implementation. It reports whether it wrote a
// new breach stamp, so SweepBreaches can count it, without changing
// EvaluateBreaches' own exported signature.
//
// A newly-detected breach is written through StampBreaches, not UpdateRecord.
// UpdateRecord is a full-row read-modify-write, and the sweep's read and
// write are far enough apart in time — up to a full pass over every open SLA
// ticket — that a staff reply recording FirstResponseAt or ResolvedAt in
// between would be silently overwritten back to NULL by a stale copy of this
// function's own read. StampBreaches only ever sets a column that is still
// NULL, so it cannot clobber a concurrent write to a column it does not own,
// and calling it a second time with the same or a later now is a no-op.
func (s *Service) evaluate(ctx context.Context, t ticket.Ticket, now time.Time) (bool, error) {
	record, err := s.store.GetRecord(ctx, t.ID)
	if errors.Is(err, ErrNoRecord) {
		return false, nil // this ticket is not under an SLA
	}
	if err != nil {
		return false, fmt.Errorf("getting SLA record: %w", err)
	}
	policy, err := s.store.GetPolicy(ctx, record.PolicyID)
	if err != nil {
		return false, fmt.Errorf("getting SLA policy: %w", err)
	}

	var response, resolution *time.Time
	if record.ResponseBreachedAt == nil && IsResponseBreached(record, policy, t, now) {
		response = &now
	}
	if record.ResolutionBreachedAt == nil && IsResolutionBreached(record, policy, t, now) {
		resolution = &now
	}
	if response == nil && resolution == nil {
		return false, nil
	}
	if err := s.store.StampBreaches(ctx, t.ID, response, resolution); err != nil {
		return false, fmt.Errorf("stamping SLA breaches: %w", err)
	}
	return true, nil
}

// TicketGetter is what SweepBreaches needs from the ticket layer: the full
// row, so whatever fields the pause-aware elapsed calculation reads (see
// sla.Elapsed) reach evaluate() rather than a partial struct built from the
// candidate query's own columns, which would silently evaluate as never
// paused.
type TicketGetter interface {
	GetByID(ctx context.Context, id uuid.UUID) (ticket.Ticket, error)
}

// SweepResult reports how many candidates a SweepBreaches pass looked at and
// how many of those it actually stamped a new breach for.
type SweepResult struct {
	Evaluated int
	Stamped   int
}

// SweepBreaches evaluates every current breach candidate once. Called on a
// schedule (see cmd/server/main.go).
//
// A failure loading or evaluating one ticket does not stop the pass; every
// such failure is collected and returned together via errors.Join, alongside
// the counts of what did succeed. The pass stops early only when ctx is
// done, between candidates, so a cancelled sweep does not start work it
// cannot finish.
func (s *Service) SweepBreaches(ctx context.Context, tickets TicketGetter, now time.Time) (SweepResult, error) {
	var res SweepResult

	ids, err := s.store.ListBreachCandidates(ctx, now)
	if err != nil {
		return res, fmt.Errorf("listing SLA breach candidates: %w", err)
	}

	var errs []error
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		t, err := tickets.GetByID(ctx, id)
		if err != nil {
			errs = append(errs, fmt.Errorf("loading ticket %s for SLA sweep: %w", id, err))
			continue
		}
		stamped, err := s.evaluate(ctx, t, now)
		if err != nil {
			errs = append(errs, fmt.Errorf("evaluating SLA breaches for ticket %s: %w", id, err))
			continue
		}
		res.Evaluated++
		if stamped {
			res.Stamped++
		}
	}
	return res, errors.Join(errs...)
}

// ── Policy CRUD ───────────────────────────────────────────────────────────────

// validatePolicy guards both doors onto sla_policies. Priority was previously
// unvalidated on either, so an unknown value travelled all the way to the
// column's CHECK constraint and the raw driver error — table name, constraint
// name, SQLSTATE — was handed back to the client as the 400's message.
func validatePolicy(p Policy) error {
	if p.Name == "" {
		return fmt.Errorf("policy name is required")
	}
	if p.Priority != nil && !p.Priority.Valid() {
		return fmt.Errorf("invalid priority %q", *p.Priority)
	}
	if p.ResponseTargetMin <= 0 {
		return fmt.Errorf("response target must be greater than zero")
	}
	if p.ResolutionTargetMin <= 0 {
		return fmt.Errorf("resolution target must be greater than zero")
	}
	return nil
}

func (s *Service) CreatePolicy(ctx context.Context, p Policy) (Policy, error) {
	if err := validatePolicy(p); err != nil {
		return Policy{}, err
	}
	p.ID = uuid.New()
	if err := s.store.CreatePolicy(ctx, p); err != nil {
		return Policy{}, fmt.Errorf("creating SLA policy: %w", err)
	}
	return p, nil
}

func (s *Service) GetPolicy(ctx context.Context, id uuid.UUID) (Policy, error) {
	return s.store.GetPolicy(ctx, id)
}

func (s *Service) UpdatePolicy(ctx context.Context, p Policy) error {
	if err := validatePolicy(p); err != nil {
		return err
	}
	return s.store.UpdatePolicy(ctx, p)
}

func (s *Service) DeletePolicy(ctx context.Context, id uuid.UUID) error {
	return s.store.DeletePolicy(ctx, id)
}

func (s *Service) ListPolicies(ctx context.Context) ([]Policy, error) {
	return s.store.ListPolicies(ctx)
}
