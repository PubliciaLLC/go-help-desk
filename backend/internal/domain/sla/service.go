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

// RecordFirstResponse marks the time of the first staff reply on a ticket and
// freezes its elapsed-toward-target reading as of that same instant (see the
// doc comment on Record.ResponseElapsedAtMetSeconds for why the number, not
// just the timestamp, has to be captured now). It is a no-op when already
// recorded, or when the ticket carries no SLA record.
//
// t is the ticket as it stood at the moment of the response — the caller
// always has this row already (it just read or wrote it), so Elapsed is
// computed here rather than via a GetRecord round trip for the ticket side.
// A GetRecord round trip IS made, but only to read the record's immutable
// policy_id (for the target) and to decide whether this call has anything to
// do; the actual write is still SetFirstResponse's single COALESCE-guarded
// UPDATE touching only its own two columns, and the breach stamp below is
// StampBreaches's own COALESCE-guarded UPDATE — so a breach stamp
// StampBreaches wrote between this call being triggered and it running still
// cannot be clobbered (see store.go's SetFirstResponse and CLAUDE.md).
//
// #217: this is also where a LATE response gets its breach stamp the moment
// it actually happens, using the frozen elapsed value just computed — not
// only from the periodic sweep evaluating an outstanding record, which misses
// a response that lands late entirely between sweep ticks (or during an
// SLA-toggle-off stretch, since #216 stops gating this method on the toggle).
func (s *Service) RecordFirstResponse(ctx context.Context, t ticket.Ticket, at time.Time) error {
	record, err := s.store.GetRecord(ctx, t.ID)
	if errors.Is(err, ErrNoRecord) {
		return nil // this ticket is not under an SLA
	}
	if err != nil {
		return fmt.Errorf("recording SLA first response: %w", err)
	}
	if record.FirstResponseAt != nil {
		return nil // already recorded; nothing to do
	}
	policy, err := s.store.GetPolicy(ctx, record.PolicyID)
	if err != nil {
		return fmt.Errorf("getting SLA policy for ticket %s: %w", t.ID, err)
	}

	elapsed := int64(Elapsed(t, at) / time.Second)
	if err := s.store.SetFirstResponse(ctx, t.ID, at, elapsed); err != nil {
		return fmt.Errorf("recording SLA first response: %w", err)
	}
	if elapsed > int64(policy.ResponseTargetMin)*60 {
		if err := s.store.StampBreaches(ctx, t.ID, &at, nil); err != nil {
			return fmt.Errorf("stamping SLA response breach: %w", err)
		}
	}
	return nil
}

// RecordResolved stamps when a ticket was resolved, so resolution breaches are
// judged against the time it was actually resolved, and freezes its
// elapsed-toward-target reading the same way RecordFirstResponse does. It is
// a no-op when a resolution is already recorded, or when the ticket carries
// no SLA record — which is also how #220's close()-calls-RecordResolved path
// stays a no-op for a ticket that was already resolved before being closed:
// only a ticket that reaches Closed WITHOUT ever resolving reaches the write
// below.
//
// Nothing used to write ResolvedAt at all. IsResolutionBreached therefore saw
// a NULL ResolvedAt on every ticket and would have reported each one as
// breached the moment its deadline passed, however promptly it had been
// resolved.
//
// #217: like RecordFirstResponse, this stamps a late RESOLUTION breach right
// here, at the instant it is recorded, rather than relying solely on the
// sweep to notice an outstanding record later.
//
// #219: a resolution is a response in every practical sense. If nothing has
// satisfied the response target yet (no prior staff reply), this resolution
// does — COALESCE-guarded via SetFirstResponse, so an actual earlier reply is
// left untouched, and its own frozen elapsed/breach decision (made at ITS
// instant, not this one) stands.
func (s *Service) RecordResolved(ctx context.Context, t ticket.Ticket, at time.Time) error {
	record, err := s.store.GetRecord(ctx, t.ID)
	if errors.Is(err, ErrNoRecord) {
		return nil // this ticket is not under an SLA
	}
	if err != nil {
		return fmt.Errorf("recording SLA resolution: %w", err)
	}
	if record.ResolvedAt != nil {
		return nil // already resolved; nothing to do
	}
	policy, err := s.store.GetPolicy(ctx, record.PolicyID)
	if err != nil {
		return fmt.Errorf("getting SLA policy for ticket %s: %w", t.ID, err)
	}

	elapsed := int64(Elapsed(t, at) / time.Second)
	if err := s.store.SetResolved(ctx, t.ID, at, elapsed); err != nil {
		return fmt.Errorf("recording SLA resolution: %w", err)
	}

	var responseBreach, resolutionBreach *time.Time
	if elapsed > int64(policy.ResolutionTargetMin)*60 {
		resolutionBreach = &at
	}

	firstResponseAlreadySet := record.FirstResponseAt != nil
	if !firstResponseAlreadySet {
		if err := s.store.SetFirstResponse(ctx, t.ID, at, elapsed); err != nil {
			return fmt.Errorf("recording SLA response at resolution: %w", err)
		}
		if elapsed > int64(policy.ResponseTargetMin)*60 {
			responseBreach = &at
		}
	}

	if responseBreach != nil || resolutionBreach != nil {
		if err := s.store.StampBreaches(ctx, t.ID, responseBreach, resolutionBreach); err != nil {
			return fmt.Errorf("stamping SLA breach at resolution: %w", err)
		}
	}
	return nil
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

// StatusesFor returns a live SLA status for every ticket that has a record
// and a policy that still exists. Tickets without one are simply absent from
// the map — no policy, no status, not a zero-value entry.
//
// The cost is exactly two store calls, however many tickets are passed: the
// records for this page (one ListRecordsByTicketIDs), and the policy table —
// a handful of rows, read once and indexed by id rather than one GetPolicy
// per record. Called after a ticket list has already been paged and sliced,
// never before, so it never computes a status that gets thrown away (see
// #183 and the "Ticket list paging" note in CLAUDE.md).
func (s *Service) StatusesFor(ctx context.Context, tickets []ticket.Ticket, now time.Time) (map[uuid.UUID]Status, error) {
	if len(tickets) == 0 {
		return map[uuid.UUID]Status{}, nil
	}

	ids := make([]uuid.UUID, len(tickets))
	byID := make(map[uuid.UUID]ticket.Ticket, len(tickets))
	for i, t := range tickets {
		ids[i] = t.ID
		byID[t.ID] = t
	}

	records, err := s.store.ListRecordsByTicketIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("listing SLA records: %w", err)
	}

	policies, err := s.store.ListPolicies(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing SLA policies: %w", err)
	}
	policyByID := make(map[uuid.UUID]Policy, len(policies))
	for _, p := range policies {
		policyByID[p.ID] = p
	}

	out := make(map[uuid.UUID]Status, len(records))
	for _, r := range records {
		p, ok := policyByID[r.PolicyID]
		if !ok {
			continue // the policy was deleted after this record was created
		}
		t, ok := byID[r.TicketID]
		if !ok {
			continue // defensive: the store was asked for exactly these ids
		}
		out[r.TicketID] = StatusFor(r, p, t, now)
	}
	return out, nil
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
