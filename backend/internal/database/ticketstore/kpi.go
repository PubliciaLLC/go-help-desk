package ticketstore

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/database"
	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/report"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// The KPI reports' queries (#131, DESIGN.md "KPI reports", SQL). Each runs on a
// *dbgen.Queries it is handed rather than on the store's own, so Store.KPI can
// run them all inside one transaction. What they share is a kpiScope.

// kpiScope is what every KPI query is filled with besides its dates: the zone,
// the visibility parameters exactly as the ticket list fills them, and the
// request's three filters.
type kpiScope struct {
	tz string

	unrestricted         bool
	reporterOnly         bool
	actorID              uuid.UUID
	scopeGroupIDs        []uuid.UUID
	scopeCategoryIDs     []uuid.UUID
	scopeTypeIDs         []uuid.UUID
	scopeTypeCategoryIDs []uuid.UUID

	categoryID uuid.NullUUID
	priorities []string // never nil: the SQL guard is cardinality()
	groupID    uuid.NullUUID
}

// kpiScope fills the scope for req. The visibility parameters come from
// filterParams, the list's own, so there is one place that reads a caller's
// groups and what they cover.
func (s *Store) kpiScope(ctx context.Context, req report.Request) (kpiScope, error) {
	p, err := s.filterParams(ctx, ticket.Filter{ActorID: req.ActorID, Visibility: req.Visibility}, req.Now)
	if err != nil {
		return kpiScope{}, fmt.Errorf("filling the report's visibility: %w", err)
	}
	return kpiScope{
		tz:                   req.Query.TZ,
		unrestricted:         p.Unrestricted,
		reporterOnly:         p.ReporterOnly,
		actorID:              p.ActorID,
		scopeGroupIDs:        p.ScopeGroupIds,
		scopeCategoryIDs:     p.ScopeCategoryIds,
		scopeTypeIDs:         p.ScopeTypeIds,
		scopeTypeCategoryIDs: p.ScopeTypeCategoryIds,
		categoryID:           database.NullUUID(req.Query.CategoryID),
		priorities:           priorityNames(req.Query.Priorities),
		groupID:              database.NullUUID(req.Query.GroupID),
	}, nil
}

// kpiPeriod is the period measures of p: what was created in it, and what
// finished in it.
func kpiPeriod(ctx context.Context, q *dbgen.Queries, sc kpiScope, p report.Period) (report.PeriodRow, error) {
	r, err := q.KPIPeriod(ctx, dbgen.KPIPeriodParams{
		Tz:                   sc.tz,
		FromDay:              p.From.Time(),
		ToDay:                p.To.Time(),
		Unrestricted:         sc.unrestricted,
		ReporterOnly:         sc.reporterOnly,
		ActorID:              sc.actorID,
		ScopeGroupIds:        sc.scopeGroupIDs,
		ScopeCategoryIds:     sc.scopeCategoryIDs,
		ScopeTypeIds:         sc.scopeTypeIDs,
		ScopeTypeCategoryIds: sc.scopeTypeCategoryIDs,
		CategoryID:           sc.categoryID,
		Priorities:           sc.priorities,
		AssigneeGroupID:      sc.groupID,
	})
	if err != nil {
		return report.PeriodRow{}, fmt.Errorf("reading the period measures %s to %s: %w", p.From, p.To, err)
	}
	return report.PeriodRow{
		Created:  r.Created,
		Resolved: r.Resolved,
		// The query cannot say NULL (sqlc has no nullable aggregate), so it
		// says 0 and the count beside each figure says whether it is one.
		ResolveMedianSeconds:       secondsIf(r.Resolved, r.ResolveMedian),
		ResolveP90Seconds:          secondsIf(r.Resolved, r.ResolveP90),
		FirstResponseCount:         r.FirstResponseCount,
		FirstResponseMedianSeconds: secondsIf(r.FirstResponseCount, r.FirstResponseMedian),
		SLATracked:                 r.SlaTracked,
		SLAMet:                     r.SlaMet,
		SLAResponseMet:             r.SlaResponseMet,
		SLAResolutionMet:           r.SlaResolutionMet,
		Reopened:                   r.Reopened,
	}, nil
}

// secondsIf is v, or nil when the population it was taken over is empty.
func secondsIf(count, v int64) *int64 {
	if count == 0 {
		return nil
	}
	return &v
}

// kpiCreatedByCategory is the tickets created in p by category, largest first.
func kpiCreatedByCategory(ctx context.Context, q *dbgen.Queries, sc kpiScope, p report.Period) ([]report.CategoryCount, error) {
	rows, err := q.KPICreatedByCategory(ctx, dbgen.KPICreatedByCategoryParams{
		Tz:                   sc.tz,
		FromDay:              p.From.Time(),
		ToDay:                p.To.Time(),
		Unrestricted:         sc.unrestricted,
		ReporterOnly:         sc.reporterOnly,
		ActorID:              sc.actorID,
		ScopeGroupIds:        sc.scopeGroupIDs,
		ScopeCategoryIds:     sc.scopeCategoryIDs,
		ScopeTypeIds:         sc.scopeTypeIDs,
		ScopeTypeCategoryIds: sc.scopeTypeCategoryIDs,
		CategoryID:           sc.categoryID,
		Priorities:           sc.priorities,
		AssigneeGroupID:      sc.groupID,
	})
	if err != nil {
		return nil, fmt.Errorf("counting created tickets by category: %w", err)
	}
	out := make([]report.CategoryCount, len(rows))
	for i, r := range rows {
		out[i] = report.CategoryCount{ID: r.CategoryID, Name: r.Name, Count: r.N}
	}
	return out, nil
}

// kpiCreatedByPriority is the tickets created in p by priority. Only the
// priorities that have tickets are rows.
func kpiCreatedByPriority(ctx context.Context, q *dbgen.Queries, sc kpiScope, p report.Period) ([]report.PriorityCount, error) {
	rows, err := q.KPICreatedByPriority(ctx, dbgen.KPICreatedByPriorityParams{
		Tz:                   sc.tz,
		FromDay:              p.From.Time(),
		ToDay:                p.To.Time(),
		Unrestricted:         sc.unrestricted,
		ReporterOnly:         sc.reporterOnly,
		ActorID:              sc.actorID,
		ScopeGroupIds:        sc.scopeGroupIDs,
		ScopeCategoryIds:     sc.scopeCategoryIDs,
		ScopeTypeIds:         sc.scopeTypeIDs,
		ScopeTypeCategoryIds: sc.scopeTypeCategoryIDs,
		CategoryID:           sc.categoryID,
		Priorities:           sc.priorities,
		AssigneeGroupID:      sc.groupID,
	})
	if err != nil {
		return nil, fmt.Errorf("counting created tickets by priority: %w", err)
	}
	out := make([]report.PriorityCount, len(rows))
	for i, r := range rows {
		out[i] = report.PriorityCount{Priority: ticket.Priority(r.Priority), Count: r.N}
	}
	return out, nil
}
