package ticketstore

import (
	"context"

	"github.com/publiciallc/go-help-desk/backend/internal/dbgen"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/report"
)

// The KPI helpers are unexported because only Store.KPI calls them, but their
// SQL is worth testing one query at a time, and the tests live in package
// ticketstore_test (internal/testutil imports this package, so an internal test
// cannot reach a database). These are the only doors between the two.

// KPIScope is the visibility and filter parameters every KPI query shares.
type KPIScope = kpiScope

func (s *Store) KPIScopeFor(ctx context.Context, req report.Request) (KPIScope, error) {
	return s.kpiScope(ctx, req)
}

func KPIPeriodRow(ctx context.Context, q *dbgen.Queries, sc KPIScope, p report.Period) (report.PeriodRow, error) {
	return kpiPeriod(ctx, q, sc, p)
}

func KPICreatedByCategoryRows(ctx context.Context, q *dbgen.Queries, sc KPIScope, p report.Period) ([]report.CategoryCount, error) {
	return kpiCreatedByCategory(ctx, q, sc, p)
}

func KPICreatedByPriorityRows(ctx context.Context, q *dbgen.Queries, sc KPIScope, p report.Period) ([]report.PriorityCount, error) {
	return kpiCreatedByPriority(ctx, q, sc, p)
}
