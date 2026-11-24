package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// GET /api/v1/reports/kpi (#131). The numbers themselves are the store's and
// the domain's (tested there); these tests hold the endpoint to its contract:
// who may call it, the shape of the answer, the order its checks run in, and
// that it counts only what the caller can see. The fixture is newListFixture
// (see handler_tickets_filters_test.go).

type kpiPeriod struct {
	From string `json:"from"`
	To   string `json:"to"`
	Days int    `json:"days"`
}

type kpiChange struct {
	Kind  string   `json:"kind"`
	State string   `json:"state"`
	Value *float64 `json:"value"`
}

type kpiMeasures struct {
	Created       int64 `json:"created"`
	Resolved      int64 `json:"resolved"`
	TimeToResolve struct {
		Count  int64  `json:"count"`
		Median *int64 `json:"median_seconds"`
		P90    *int64 `json:"p90_seconds"`
	} `json:"time_to_resolve"`
	FirstResponse struct {
		Count  int64  `json:"count"`
		Median *int64 `json:"median_seconds"`
	} `json:"first_response"`
	SLA struct {
		Tracked           int64    `json:"tracked"`
		Met               int64    `json:"met"`
		ResponseMet       int64    `json:"response_met"`
		ResolutionMet     int64    `json:"resolution_met"`
		MetRate           *float64 `json:"met_rate"`
		ResponseMetRate   *float64 `json:"response_met_rate"`
		ResolutionMetRate *float64 `json:"resolution_met_rate"`
	} `json:"sla"`
	Reopen struct {
		Reopened int64    `json:"reopened"`
		Rate     *float64 `json:"rate"`
	} `json:"reopen"`
}

type kpiHolder struct {
	ID    *string `json:"id"`
	Name  *string `json:"name"`
	Count int64   `json:"count"`
}

type kpiBody struct {
	TZ      string    `json:"tz"`
	Period  kpiPeriod `json:"period"`
	Prev    kpiPeriod `json:"previous"`
	Filters struct {
		CategoryID *string  `json:"category_id"`
		Priority   []string `json:"priority"`
		GroupID    *string  `json:"assignee_group_id"`
	} `json:"filters"`
	SLAEnabled bool `json:"sla_enabled"`
	Measures   struct {
		Current  kpiMeasures          `json:"current"`
		Previous kpiMeasures          `json:"previous"`
		Change   map[string]kpiChange `json:"change"`
	} `json:"measures"`
	Volume struct {
		Days []struct {
			Date     string `json:"date"`
			Created  int64  `json:"created"`
			Resolved int64  `json:"resolved"`
		} `json:"days"`
		ByCategory []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Count int64  `json:"count"`
		} `json:"by_category"`
		ByPriority []struct {
			Priority string `json:"priority"`
			Count    int64  `json:"count"`
		} `json:"by_priority"`
	} `json:"volume"`
	Backlog struct {
		AsOf    time.Time `json:"as_of"`
		Total   int64     `json:"total"`
		Buckets []struct {
			Key     string `json:"key"`
			MinDays int    `json:"min_days"`
			MaxDays *int   `json:"max_days"`
			Count   int64  `json:"count"`
		} `json:"buckets"`
	} `json:"backlog"`
	Workload struct {
		AsOf       time.Time   `json:"as_of"`
		Total      int64       `json:"total"`
		ByAssignee []kpiHolder `json:"by_assignee"`
		ByGroup    []kpiHolder `json:"by_group"`
	} `json:"workload"`
}

type kpiResult struct {
	status  int
	raw     []byte
	code    string
	message string
	kpiBody
}

func (k kpiResult) createdInPeriod() int64 {
	var n int64
	for _, d := range k.Volume.Days {
		n += d.Created
	}
	return n
}

var kpiChangeKeys = []string{
	"created", "resolved", "time_to_resolve_median", "time_to_resolve_p90", "first_response_median",
	"sla_met_rate", "sla_response_met_rate", "sla_resolution_met_rate", "reopen_rate",
}

func readKPI(t *testing.T, res *http.Response) kpiResult {
	t.Helper()
	raw := readAll(t, res)
	k := kpiResult{status: res.StatusCode, raw: raw}
	if res.StatusCode != http.StatusOK {
		var e struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		k.code, k.message = e.Error.Code, e.Error.Message
		return k
	}
	require.NoError(t, json.Unmarshal(raw, &k.kpiBody), "body: %s", raw)
	return k
}

func (h *harness) kpiAs(t *testing.T, as, query string) kpiResult {
	t.Helper()
	path := "/api/v1/reports/kpi" + query
	switch as {
	case "admin":
		return readKPI(t, h.doAsAdmin(t, http.MethodGet, path, nil))
	case "user":
		return readKPI(t, h.doAsUser(t, http.MethodGet, path, nil))
	}
	return readKPI(t, h.do(t, http.MethodGet, path, nil))
}

func TestKPI_ResponseShape(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	newListFixture(t, h)

	for _, as := range []string{"admin", "staff"} {
		t.Run(as, func(t *testing.T) {
			k := h.kpiAs(t, as, "")
			require.Equal(t, http.StatusOK, k.status, "body: %s", k.raw)

			require.Equal(t, "UTC", k.TZ)
			require.Equal(t, 30, k.Period.Days)
			require.Equal(t, 30, k.Prev.Days)
			require.Len(t, k.Volume.Days, 30)
			require.Equal(t, k.Period.From, k.Volume.Days[0].Date)
			require.Equal(t, k.Period.To, k.Volume.Days[29].Date)
			from, err := time.Parse("2006-01-02", k.Period.From)
			require.NoError(t, err)
			require.Equal(t, from.AddDate(0, 0, -1).Format("2006-01-02"), k.Prev.To, "the previous period ends the day before")

			require.Len(t, k.Measures.Change, 9, "body: %s", k.raw)
			for _, key := range kpiChangeKeys {
				c, ok := k.Measures.Change[key]
				require.True(t, ok, "no %q change in %s", key, k.raw)
				require.NotEmpty(t, c.Kind)
				require.NotEmpty(t, c.State)
			}
			require.Equal(t, "relative", k.Measures.Change["created"].Kind)
			require.Equal(t, "points", k.Measures.Change["reopen_rate"].Kind)
			require.EqualValues(t, 8, k.Measures.Current.Created)
			require.EqualValues(t, 8, k.createdInPeriod())
			// Nothing was created before the period.
			require.Zero(t, k.Measures.Previous.Created)
			require.Equal(t, "from_zero", k.Measures.Change["created"].State)
			require.Nil(t, k.Measures.Change["created"].Value)

			require.Nil(t, k.Filters.CategoryID)
			require.NotNil(t, k.Filters.Priority, "an empty list, not null")
			require.Empty(t, k.Filters.Priority)
			require.Nil(t, k.Filters.GroupID)
			require.False(t, k.SLAEnabled)

			require.Len(t, k.Volume.ByPriority, 4)
			for i, p := range []string{"critical", "high", "medium", "low"} {
				require.Equal(t, p, k.Volume.ByPriority[i].Priority)
			}
			require.Len(t, k.Volume.ByCategory, 1)

			require.Len(t, k.Backlog.Buckets, 4)
			for i, key := range []string{"0_2", "3_7", "8_30", "over_30"} {
				require.Equal(t, key, k.Backlog.Buckets[i].Key)
			}
			require.Nil(t, k.Backlog.Buckets[3].MaxDays)
			require.EqualValues(t, 8, k.Backlog.Total)
			require.EqualValues(t, 8, k.Workload.Total)
			require.WithinDuration(t, time.Now(), k.Backlog.AsOf, time.Minute)
			require.True(t, k.Backlog.AsOf.Equal(k.Workload.AsOf), "one instant for both snapshots")

			require.NotEmpty(t, k.Workload.ByAssignee)
			last := k.Workload.ByAssignee[len(k.Workload.ByAssignee)-1]
			require.Nil(t, last.ID, "the row of tickets with nobody on them is last")
			require.Nil(t, last.Name)
			require.NotNil(t, k.Workload.ByGroup)

			var top map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(k.raw, &top))
			for _, key := range []string{"tz", "period", "previous", "filters", "sla_enabled", "measures", "volume", "backlog", "workload"} {
				require.Contains(t, top, key)
			}
		})
	}
}

func TestKPI_NullsWhereThereIsNoData(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	k := h.kpiAs(t, "admin", "")
	require.Equal(t, http.StatusOK, k.status, "body: %s", k.raw)
	require.Nil(t, k.Measures.Current.TimeToResolve.Median)
	require.Nil(t, k.Measures.Current.SLA.MetRate)
	require.Nil(t, k.Measures.Current.Reopen.Rate)
	require.Equal(t, "no_current", k.Measures.Change["time_to_resolve_median"].State)
	require.Equal(t, "no_current", k.Measures.Change["reopen_rate"].State)
	require.NotNil(t, k.Volume.ByCategory, "an empty list, not null")
	require.NotNil(t, k.Workload.ByAssignee)
	require.NotNil(t, k.Workload.ByGroup)
}

func TestKPI_Periods(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	newListFixture(t, h)

	today := time.Now().UTC().Format("2006-01-02")
	cases := []struct {
		name  string
		query string
		days  int
		to    string
	}{
		{name: "a week", query: "?from=2026-01-01&to=2026-01-07", days: 7, to: "2026-01-07"},
		{name: "one day", query: "?from=2026-01-01&to=2026-01-01", days: 1, to: "2026-01-01"},
		{name: "366 days", query: "?from=2025-01-01&to=2026-01-01", days: 366, to: "2026-01-01"},
		{name: "empty values are absent", query: "?from=&to=", days: 30, to: today},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k := h.kpiAs(t, "staff", tc.query)
			require.Equal(t, http.StatusOK, k.status, "body: %s", k.raw)
			require.Equal(t, tc.days, k.Period.Days)
			require.Len(t, k.Volume.Days, tc.days)
			require.Equal(t, tc.days, k.Prev.Days)
			if tc.name != "empty values are absent" {
				require.Equal(t, tc.to, k.Period.To)
			}
		})
	}

	t.Run("a period in the past counts none of today's tickets", func(t *testing.T) {
		k := h.kpiAs(t, "admin", "?from=2020-01-01&to=2020-01-31")
		require.Equal(t, http.StatusOK, k.status, "body: %s", k.raw)
		require.Zero(t, k.Measures.Current.Created)
		require.EqualValues(t, 8, k.Backlog.Total, "the backlog is as of now, not of the period")
	})
	t.Run("a to after today is allowed", func(t *testing.T) {
		k := h.kpiAs(t, "admin", "?from="+today+"&to=2999-01-01")
		require.Equal(t, http.StatusBadRequest, k.status, "that is longer than 366 days")
		future := time.Now().UTC().AddDate(0, 0, 5).Format("2006-01-02")
		k = h.kpiAs(t, "admin", "?from="+today+"&to="+future)
		require.Equal(t, http.StatusOK, k.status, "body: %s", k.raw)
		require.EqualValues(t, 8, k.Measures.Current.Created)
	})
}

func TestKPI_RequesterIsRefused(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	res := h.doAsUser(t, http.MethodGet, "/api/v1/reports/kpi", nil)
	require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", readAll(t, res))
}

func TestKPI_NeedsTicketsRead(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	t.Run("a key without tickets:read", func(t *testing.T) {
		key := mintKey(t, h, []string{"users:read"})
		res := withKey(t, h, key, http.MethodGet, "/api/v1/reports/kpi", nil)
		require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", readAll(t, res))
	})
	t.Run("a key with tickets:read", func(t *testing.T) {
		key := mintKey(t, h, []string{"tickets:read"})
		k := readKPI(t, withKey(t, h, key, http.MethodGet, "/api/v1/reports/kpi", nil))
		require.Equal(t, http.StatusOK, k.status, "body: %s", k.raw)
		require.Len(t, k.Measures.Change, 9)
	})
	t.Run("no credential", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/reports/kpi", nil)
		rec := httptest.NewRecorder()
		h.srv.ServeHTTP(rec, req)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}

func TestKPI_BadRequests(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	id := uuid.NewString()

	cases := []struct {
		name    string
		query   string
		message string
	}{
		{"bad tz characters", "?tz=" + url.QueryEscape("Europe London"), "invalid tz"},
		{"tz too long", "?tz=" + strings.Repeat("a", 65), "invalid tz"},
		{"from without to", "?from=2026-01-01", "from and to go together"},
		{"to without from", "?to=2026-01-01", "from and to go together"},
		{"malformed from", "?from=yesterday&to=2026-01-01", "invalid from"},
		{"malformed to", "?from=2026-01-01&to=2026-1-1", "invalid to"},
		{"from after to", "?from=2026-01-02&to=2026-01-01", "from must not be after to"},
		{"367 days", "?from=2025-01-01&to=2026-01-02", "the range is longer than 366 days"},
		{"bad category_id", "?category_id=nope", "invalid category_id"},
		{"bad priority", "?priority=urgent", "invalid priority"},
		{"five priorities", "?priority=low&priority=low&priority=low&priority=low&priority=low", "too many priority values"},
		{"bad assignee_group_id", "?assignee_group_id=" + id + "x", "invalid assignee_group_id"},
	}
	for _, tc := range cases {
		for _, as := range []string{"staff", "admin"} {
			t.Run(tc.name+" as "+as, func(t *testing.T) {
				k := h.kpiAs(t, as, tc.query)
				require.Equal(t, http.StatusBadRequest, k.status, "body: %s", k.raw)
				require.Equal(t, "bad_request", k.code)
				require.Equal(t, tc.message, k.message)
			})
		}
	}

	t.Run("an unknown parameter is ignored", func(t *testing.T) {
		k := h.kpiAs(t, "staff", "?colour=blue")
		require.Equal(t, http.StatusOK, k.status, "body: %s", k.raw)
	})
}

// A request wrong in several ways gets the first message in the handler's
// order: a malformed tz, then the query's own order, then the zone, then the
// range (which cannot be judged without today).
func TestKPI_ChecksRunInOrder(t *testing.T) {
	t.Run("a bad tz beats a bad from", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		k := h.kpiAs(t, "staff", "?tz="+url.QueryEscape("a b")+"&from=nope&to=2026-01-01")
		require.Equal(t, http.StatusBadRequest, k.status)
		require.Equal(t, "invalid tz", k.message)
	})
	t.Run("a bad from beats an unknown zone", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		k := h.kpiAs(t, "staff", "?tz=Mars/Olympus_Mons&from=nope&to=2026-01-01")
		require.Equal(t, http.StatusBadRequest, k.status)
		require.Equal(t, "invalid from", k.message)
	})
	t.Run("an unknown zone beats a range that is too long", func(t *testing.T) {
		// Postgres refuses the name inside the harness transaction, which a
		// failed statement leaves dead, so it has a harness of its own.
		h, cleanup := newHarness(t)
		defer cleanup()
		k := h.kpiAs(t, "staff", "?tz=Mars/Olympus_Mons&from=2020-01-01&to=2026-01-01")
		require.Equal(t, http.StatusBadRequest, k.status, "body: %s", k.raw)
		require.Equal(t, "unknown time zone", k.message)
	})
}

func TestKPI_TZ(t *testing.T) {
	t.Run("a zone is echoed and used", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		k := h.kpiAs(t, "staff", "?tz=Pacific/Kiritimati")
		require.Equal(t, http.StatusOK, k.status, "body: %s", k.raw)
		require.Equal(t, "Pacific/Kiritimati", k.TZ)
		require.Equal(t, 30, k.Period.Days)
	})
	for _, tz := range []string{"Mars/Olympus_Mons", strings.Repeat("a", 64)} {
		t.Run("unknown to Postgres: "+tz[:min(len(tz), 12)], func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			k := h.kpiAs(t, "admin", "?tz="+url.QueryEscape(tz))
			require.Equal(t, http.StatusBadRequest, k.status, "body: %s", k.raw)
			require.Equal(t, "bad_request", k.code)
			require.Equal(t, "unknown time zone", k.message)
		})
	}
}

func TestKPI_ScopeEnforcementNarrowsEveryNumber(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	fx := newListFixture(t, h)

	off := h.kpiAs(t, "staff", "")
	require.Equal(t, http.StatusOK, off.status, "body: %s", off.raw)
	adm := h.kpiAs(t, "admin", "")
	require.EqualValues(t, 8, off.Measures.Current.Created)
	require.Equal(t, adm.Measures.Current, off.Measures.Current, "with enforcement off staff see what the administrator sees")
	require.Equal(t, adm.Backlog.Total, off.Backlog.Total)
	require.Equal(t, adm.Workload.Total, off.Workload.Total)

	enableScope(t, h)
	// The staff member's group covers no category: what they reported, what is
	// assigned to them and what is assigned to their group (a, b, e, f).
	narrow := h.kpiAs(t, "staff", "")
	require.Equal(t, http.StatusOK, narrow.status, "body: %s", narrow.raw)
	require.EqualValues(t, 4, narrow.Measures.Current.Created)
	require.EqualValues(t, 4, narrow.createdInPeriod())
	require.EqualValues(t, 4, narrow.Backlog.Total)
	require.EqualValues(t, 4, narrow.Workload.Total)

	adminOn := h.kpiAs(t, "admin", "")
	require.EqualValues(t, 8, adminOn.Measures.Current.Created, "an administrator is never scoped")
	require.EqualValues(t, 8, adminOn.Backlog.Total)

	// A category the group covers brings the rest back.
	require.NoError(t, h.groupSvc.AddScope(context.Background(), group.GroupScope{GroupID: fx.mine, CategoryID: h.catID}))
	covered := h.kpiAs(t, "staff", "")
	require.EqualValues(t, 8, covered.Measures.Current.Created)
}

func TestKPI_FiltersAreEchoedAndApplied(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	fx := newListFixture(t, h)
	ctx := context.Background()

	// Two tickets at known priorities, so a filter has something to cut.
	for _, p := range []ticket.Priority{ticket.PriorityCritical, ticket.PriorityLow} {
		_, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
			DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
			Subject: "p " + string(p), CategoryID: h.catID, Priority: p, ReporterUserID: &h.userID,
		})
		require.NoError(t, err)
	}

	all := h.kpiAs(t, "admin", "")
	require.EqualValues(t, 10, all.Measures.Current.Created)

	t.Run("priority is echoed in the fixed order and narrows", func(t *testing.T) {
		k := h.kpiAs(t, "admin", "?priority=low&priority=critical")
		require.Equal(t, http.StatusOK, k.status, "body: %s", k.raw)
		require.Equal(t, []string{"critical", "low"}, k.Filters.Priority)
		var n int64
		for _, p := range k.Volume.ByPriority {
			if p.Priority == "high" || p.Priority == "medium" {
				n += p.Count
			}
		}
		require.Zero(t, n)
		require.Less(t, k.Measures.Current.Created, all.Measures.Current.Created)
		require.GreaterOrEqual(t, k.Measures.Current.Created, int64(2))
	})
	t.Run("category_id is echoed", func(t *testing.T) {
		k := h.kpiAs(t, "admin", "?category_id="+h.catID.String())
		require.Equal(t, http.StatusOK, k.status, "body: %s", k.raw)
		require.NotNil(t, k.Filters.CategoryID)
		require.Equal(t, h.catID.String(), *k.Filters.CategoryID)
		require.EqualValues(t, 10, k.Measures.Current.Created)

		other := h.kpiAs(t, "admin", "?category_id="+uuid.NewString())
		require.Equal(t, http.StatusOK, other.status, "body: %s", other.raw)
		require.Zero(t, other.Measures.Current.Created)
		require.Zero(t, other.Backlog.Total, "filters narrow the snapshots too")
	})
	t.Run("assignee_group_id is echoed and narrows", func(t *testing.T) {
		k := h.kpiAs(t, "admin", "?assignee_group_id="+fx.mine.String())
		require.Equal(t, http.StatusOK, k.status, "body: %s", k.raw)
		require.NotNil(t, k.Filters.GroupID)
		require.Equal(t, fx.mine.String(), *k.Filters.GroupID)
		require.EqualValues(t, 1, k.Measures.Current.Created, "ticket b")
	})
}

func TestKPI_SLAFiguresAreComputedWhileTrackingIsOff(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	enableSLAWithShortPolicy(t, h)
	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		Subject: "tracked", CategoryID: h.catID, Priority: ticket.PriorityLow, ReporterUserID: &h.userID,
	})
	require.NoError(t, err)
	_, err = h.ticketSvc.Resolve(ctx, tk.ID, "done", ticket.Actor{UserID: &h.staffID, Role: user.RoleStaff})
	require.NoError(t, err)

	on := h.kpiAs(t, "admin", "")
	require.Equal(t, http.StatusOK, on.status, "body: %s", on.raw)
	require.True(t, on.SLAEnabled)
	require.EqualValues(t, 1, on.Measures.Current.SLA.Tracked, "body: %s", on.raw)

	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeySLAEnabled, false))
	off := h.kpiAs(t, "admin", "")
	require.Equal(t, http.StatusOK, off.status, "body: %s", off.raw)
	require.False(t, off.SLAEnabled)
	require.Equal(t, on.Measures.Current.SLA, off.Measures.Current.SLA, "the figures come from the records that exist")
}
