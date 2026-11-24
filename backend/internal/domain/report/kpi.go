package report

import (
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// Measures are the period measures of one period. A median or percentile is
// nil when its population is empty, and a rate is nil when its denominator is
// 0; the counts they came from sit beside them.
type Measures struct {
	Created       int64
	Resolved      int64
	TimeToResolve TimeToResolve
	FirstResponse FirstResponse
	SLA           SLA
	Reopen        Reopen
}

// TimeToResolve is the time from creation to finish, in seconds, less pending
// time. Count is the number of finished tickets it is taken over.
type TimeToResolve struct {
	Count         int64
	MedianSeconds *int64
	P90Seconds    *int64
}

// FirstResponse is the time to the first SLA response, in seconds, over the
// tickets that have one.
type FirstResponse struct {
	Count         int64
	MedianSeconds *int64
}

// SLA is attainment over the tickets that were tracked when they finished.
type SLA struct {
	Tracked           int64
	Met               int64
	ResponseMet       int64
	ResolutionMet     int64
	MetRate           *float64
	ResponseMetRate   *float64
	ResolutionMetRate *float64
}

// Reopen counts the finished tickets that were reopened at least once.
type Reopen struct {
	Reopened int64
	Rate     *float64
}

// KPI is the answer for one request: both periods' measures, the comparison
// of the nine compared measures keyed by their Compared key, and the current
// period's breakdowns.
type KPI struct {
	Current  Measures
	Previous Measures
	Change   map[string]Change
	Volume   Volume
	Backlog  Backlog
	Workload Workload
}

// Volume is the current period's created and resolved counts by day, and the
// created counts by category and by priority.
type Volume struct {
	Days       []ticket.DayVolume
	ByCategory []CategoryCount
	ByPriority []PriorityCount
}

// CategoryCount is the tickets created in a category, named as it is now.
type CategoryCount struct {
	ID    uuid.UUID
	Name  string
	Count int64
}

// PriorityCount is the tickets created at one priority.
type PriorityCount struct {
	Priority ticket.Priority
	Count    int64
}

// Backlog is the open tickets by age.
type Backlog struct {
	Total   int64
	Buckets []BucketCount
}

// Workload is the open tickets by the person and by the group holding them.
// A nil Holder ID and Name is the row of tickets with none.
type Workload struct {
	Total      int64
	ByAssignee []Holder
	ByGroup    []Holder
}

// Holder is one row of the workload: a person or a group, or nobody and no
// group (ID and Name nil).
type Holder struct {
	ID    *uuid.UUID
	Name  *string
	Count int64
}

// Request is everything one KPI run needs: who is asking and what they may
// see, the parsed query, both periods and the instant "now" was read at.
type Request struct {
	ActorID    uuid.UUID
	Visibility ticket.Visibility
	Query      Query
	Current    Period
	Previous   Period
	Now        time.Time
}

// PeriodRow is the one row KPIPeriod returns for a period: counts, and the
// median and percentile in seconds, nil when their population is empty.
type PeriodRow struct {
	Created                    int64
	Resolved                   int64
	ResolveMedianSeconds       *int64
	ResolveP90Seconds          *int64
	FirstResponseCount         int64
	FirstResponseMedianSeconds *int64
	SLATracked                 int64
	SLAMet                     int64
	SLAResponseMet             int64
	SLAResolutionMet           int64
	Reopened                   int64
}

// Raw is the unprocessed rows of the eight queries of one request, read in
// one transaction: both periods' rows, the current period's daily volume and
// created counts by category and priority, the open tickets by age, and the
// open tickets by assignee and by group. Service.KPI turns it into a KPI.
type Raw struct {
	Current    PeriodRow
	Previous   PeriodRow
	Days       []ticket.DayVolume
	ByCategory []CategoryCount
	ByPriority []PriorityCount
	Ages       []AgeCount
	ByAssignee []Holder
	ByGroup    []Holder
}
