package server

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/report"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

type kpiPeriodJSON struct {
	From string `json:"from"`
	To   string `json:"to"`
	Days int    `json:"days"`
}

type kpiFiltersJSON struct {
	CategoryID *uuid.UUID        `json:"category_id"`
	Priority   []ticket.Priority `json:"priority"`
	GroupID    *uuid.UUID        `json:"assignee_group_id"`
}

type kpiTimeToResolveJSON struct {
	Count         int64  `json:"count"`
	MedianSeconds *int64 `json:"median_seconds"`
	P90Seconds    *int64 `json:"p90_seconds"`
}

type kpiFirstResponseJSON struct {
	Count         int64  `json:"count"`
	MedianSeconds *int64 `json:"median_seconds"`
}

type kpiSLAJSON struct {
	Tracked           int64    `json:"tracked"`
	Met               int64    `json:"met"`
	ResponseMet       int64    `json:"response_met"`
	ResolutionMet     int64    `json:"resolution_met"`
	MetRate           *float64 `json:"met_rate"`
	ResponseMetRate   *float64 `json:"response_met_rate"`
	ResolutionMetRate *float64 `json:"resolution_met_rate"`
}

type kpiReopenJSON struct {
	Reopened int64    `json:"reopened"`
	Rate     *float64 `json:"rate"`
}

type kpiMeasuresJSON struct {
	Created       int64                `json:"created"`
	Resolved      int64                `json:"resolved"`
	TimeToResolve kpiTimeToResolveJSON `json:"time_to_resolve"`
	FirstResponse kpiFirstResponseJSON `json:"first_response"`
	SLA           kpiSLAJSON           `json:"sla"`
	Reopen        kpiReopenJSON        `json:"reopen"`
}

type kpiChangeJSON struct {
	Kind  report.ChangeKind  `json:"kind"`
	State report.ChangeState `json:"state"`
	Value *float64           `json:"value"`
}

type kpiMeasuresBlockJSON struct {
	Current  kpiMeasuresJSON          `json:"current"`
	Previous kpiMeasuresJSON          `json:"previous"`
	Change   map[string]kpiChangeJSON `json:"change"`
}

type kpiCategoryJSON struct {
	ID    uuid.UUID `json:"id"`
	Name  string    `json:"name"`
	Count int64     `json:"count"`
}

type kpiPriorityJSON struct {
	Priority ticket.Priority `json:"priority"`
	Count    int64           `json:"count"`
}

type kpiVolumeJSON struct {
	Days       []ticket.DayVolume `json:"days"`
	ByCategory []kpiCategoryJSON  `json:"by_category"`
	ByPriority []kpiPriorityJSON  `json:"by_priority"`
}

type kpiBucketJSON struct {
	Key     string `json:"key"`
	MinDays int    `json:"min_days"`
	MaxDays *int   `json:"max_days"`
	Count   int64  `json:"count"`
}

type kpiBacklogJSON struct {
	AsOf    time.Time       `json:"as_of"`
	Total   int64           `json:"total"`
	Buckets []kpiBucketJSON `json:"buckets"`
}

type kpiHolderJSON struct {
	ID    *uuid.UUID `json:"id"`
	Name  *string    `json:"name"`
	Count int64      `json:"count"`
}

type kpiWorkloadJSON struct {
	AsOf       time.Time       `json:"as_of"`
	Total      int64           `json:"total"`
	ByAssignee []kpiHolderJSON `json:"by_assignee"`
	ByGroup    []kpiHolderJSON `json:"by_group"`
}

// kpiResponse is DESIGN.md's response of GET /reports/kpi. Every list is sent
// as [] when empty, never null; the nulls are the fields the design names.
type kpiResponse struct {
	TZ         string               `json:"tz"`
	Period     kpiPeriodJSON        `json:"period"`
	Previous   kpiPeriodJSON        `json:"previous"`
	Filters    kpiFiltersJSON       `json:"filters"`
	SLAEnabled bool                 `json:"sla_enabled"`
	Measures   kpiMeasuresBlockJSON `json:"measures"`
	Volume     kpiVolumeJSON        `json:"volume"`
	Backlog    kpiBacklogJSON       `json:"backlog"`
	Workload   kpiWorkloadJSON      `json:"workload"`
}

// handleGetKPI is GET /api/v1/reports/kpi (#131). The checks run in a fixed
// order and the first failure answers, so a request wrong in several ways gets
// the same message every time (see DESIGN.md, KPI reports).
func (s *Server) handleGetKPI(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	ctx := r.Context()
	fail := func(err error) {
		slog.ErrorContext(ctx, "kpi report", "error", err)
		Error(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
	}

	tz, err := parseTZ(r)
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	query, err := report.ParseQuery(r.URL.Query())
	if err != nil {
		writeFilterError(w, err)
		return
	}
	query.TZ = tz

	vis, err := s.TicketVisibility(ctx, a)
	if err != nil {
		fail(err)
		return
	}

	// Read once: it is the day's end for Today and the instant of both
	// snapshots.
	now := time.Now()
	today, err := s.reports.Today(ctx, tz, now)
	if errors.Is(err, ticket.ErrUnknownTimeZone) {
		Error(w, http.StatusBadRequest, "bad_request", "unknown time zone")
		return
	}
	if err != nil {
		fail(err)
		return
	}
	cur, err := query.Period(today)
	if err != nil {
		writeFilterError(w, err)
		return
	}
	prev := cur.Previous()

	// A read failure is "off" for this response, as on the dashboard; the
	// figures are computed either way.
	slaOn, err := s.adminSvc.SLAEnabled(ctx)
	if err != nil {
		slog.WarnContext(ctx, "reading SLA enabled setting failed; reporting SLA tracking as off", "error", err)
		slaOn = false
	}

	k, err := s.reports.KPI(ctx, report.Request{
		ActorID:    a.UserID,
		Visibility: vis,
		Query:      query,
		Current:    cur,
		Previous:   prev,
		Now:        now,
	})
	if err != nil {
		fail(err)
		return
	}
	JSON(w, http.StatusOK, kpiResponseOf(tz, cur, prev, query, slaOn, now.UTC(), k))
}

func kpiResponseOf(tz string, cur, prev report.Period, q report.Query, slaOn bool, asOf time.Time, k report.KPI) kpiResponse {
	change := make(map[string]kpiChangeJSON, len(k.Change))
	for key, c := range k.Change {
		change[key] = kpiChangeJSON{Kind: c.Kind, State: c.State, Value: c.Value}
	}
	buckets := make([]kpiBucketJSON, len(k.Backlog.Buckets))
	for i, b := range k.Backlog.Buckets {
		buckets[i] = kpiBucketJSON{Key: b.Key, MinDays: b.MinDays, MaxDays: b.MaxDays, Count: b.Count}
	}
	categories := make([]kpiCategoryJSON, len(k.Volume.ByCategory))
	for i, c := range k.Volume.ByCategory {
		categories[i] = kpiCategoryJSON{ID: c.ID, Name: c.Name, Count: c.Count}
	}
	priorities := make([]kpiPriorityJSON, len(k.Volume.ByPriority))
	for i, p := range k.Volume.ByPriority {
		priorities[i] = kpiPriorityJSON{Priority: p.Priority, Count: p.Count}
	}
	days := k.Volume.Days
	if days == nil {
		days = []ticket.DayVolume{}
	}
	filterPriority := q.Priorities
	if filterPriority == nil {
		filterPriority = []ticket.Priority{}
	}
	return kpiResponse{
		TZ:         tz,
		Period:     kpiPeriodOf(cur),
		Previous:   kpiPeriodOf(prev),
		Filters:    kpiFiltersJSON{CategoryID: q.CategoryID, Priority: filterPriority, GroupID: q.GroupID},
		SLAEnabled: slaOn,
		Measures: kpiMeasuresBlockJSON{
			Current:  kpiMeasuresOf(k.Current),
			Previous: kpiMeasuresOf(k.Previous),
			Change:   change,
		},
		Volume:   kpiVolumeJSON{Days: days, ByCategory: categories, ByPriority: priorities},
		Backlog:  kpiBacklogJSON{AsOf: asOf, Total: k.Backlog.Total, Buckets: buckets},
		Workload: kpiWorkloadJSON{AsOf: asOf, Total: k.Workload.Total, ByAssignee: kpiHoldersOf(k.Workload.ByAssignee), ByGroup: kpiHoldersOf(k.Workload.ByGroup)},
	}
}

func kpiPeriodOf(p report.Period) kpiPeriodJSON {
	return kpiPeriodJSON{From: p.From.String(), To: p.To.String(), Days: p.Days()}
}

func kpiMeasuresOf(m report.Measures) kpiMeasuresJSON {
	return kpiMeasuresJSON{
		Created:       m.Created,
		Resolved:      m.Resolved,
		TimeToResolve: kpiTimeToResolveJSON{Count: m.TimeToResolve.Count, MedianSeconds: m.TimeToResolve.MedianSeconds, P90Seconds: m.TimeToResolve.P90Seconds},
		FirstResponse: kpiFirstResponseJSON{Count: m.FirstResponse.Count, MedianSeconds: m.FirstResponse.MedianSeconds},
		SLA: kpiSLAJSON{
			Tracked: m.SLA.Tracked, Met: m.SLA.Met, ResponseMet: m.SLA.ResponseMet, ResolutionMet: m.SLA.ResolutionMet,
			MetRate: m.SLA.MetRate, ResponseMetRate: m.SLA.ResponseMetRate, ResolutionMetRate: m.SLA.ResolutionMetRate,
		},
		Reopen: kpiReopenJSON{Reopened: m.Reopen.Reopened, Rate: m.Reopen.Rate},
	}
}

func kpiHoldersOf(hs []report.Holder) []kpiHolderJSON {
	out := make([]kpiHolderJSON, len(hs))
	for i, h := range hs {
		out[i] = kpiHolderJSON{ID: h.ID, Name: h.Name, Count: h.Count}
	}
	return out
}
