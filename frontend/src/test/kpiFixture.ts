import type { KpiResponse, MeasureChange } from '@/api/types'

const ch = (kind: MeasureChange['kind'], state: MeasureChange['state'], value: number | null): MeasureChange => ({
  kind,
  state,
  value,
})

// What GET /reports/kpi answers for a period with data in every card: the
// example in docs/DESIGN.md, "KPI reports".
export function kpiResponse(over: Partial<KpiResponse> = {}): KpiResponse {
  return {
    tz: 'Europe/London',
    period: { from: '2026-10-26', to: '2026-11-24', days: 30 },
    previous: { from: '2026-09-26', to: '2026-10-25', days: 30 },
    filters: { category_id: null, priority: [], assignee_group_id: null },
    sla_enabled: true,
    measures: {
      current: {
        created: 84,
        resolved: 79,
        time_to_resolve: { count: 79, median_seconds: 52200, p90_seconds: 345600 },
        first_response: { count: 61, median_seconds: 3900 },
        sla: {
          tracked: 61,
          met: 52,
          response_met: 57,
          resolution_met: 54,
          met_rate: 0.8525,
          response_met_rate: 0.9344,
          resolution_met_rate: 0.8852,
        },
        reopen: { reopened: 4, rate: 0.0506 },
      },
      previous: {
        created: 72,
        resolved: 70,
        time_to_resolve: { count: 70, median_seconds: 55000, p90_seconds: 300000 },
        first_response: { count: 55, median_seconds: 4200 },
        sla: {
          tracked: 55,
          met: 44,
          response_met: 50,
          resolution_met: 48,
          met_rate: 0.8,
          response_met_rate: 0.9091,
          resolution_met_rate: 0.8727,
        },
        reopen: { reopened: 0, rate: 0 },
      },
      change: {
        created: ch('relative', 'change', 0.1667),
        resolved: ch('relative', 'change', 0.1286),
        time_to_resolve_median: ch('relative', 'change', -0.0509),
        time_to_resolve_p90: ch('relative', 'change', 0.152),
        first_response_median: ch('relative', 'change', -0.0714),
        sla_met_rate: ch('points', 'change', 0.0532),
        sla_response_met_rate: ch('points', 'change', 0.0253),
        sla_resolution_met_rate: ch('points', 'change', 0.0125),
        reopen_rate: ch('points', 'change', 0.0506),
      },
    },
    volume: {
      days: [
        { date: '2026-10-26', created: 3, resolved: 2 },
        { date: '2026-10-27', created: 4, resolved: 1 },
      ],
      by_category: [
        { id: 'cat-1', name: 'Hardware', count: 31 },
        { id: 'cat-2', name: 'Network', count: 20 },
      ],
      by_priority: [
        { priority: 'critical', count: 2 },
        { priority: 'high', count: 9 },
        { priority: 'medium', count: 60 },
        { priority: 'low', count: 13 },
      ],
    },
    backlog: {
      as_of: '2026-11-24T10:15:00Z',
      total: 42,
      buckets: [
        { key: '0_2', min_days: 0, max_days: 2, count: 17 },
        { key: '3_7', min_days: 3, max_days: 7, count: 11 },
        { key: '8_30', min_days: 8, max_days: 30, count: 10 },
        { key: 'over_30', min_days: 31, max_days: null, count: 4 },
      ],
    },
    workload: {
      as_of: '2026-11-24T10:15:00Z',
      total: 42,
      by_assignee: [
        { id: 'u1', name: 'Ada Lovelace', count: 9 },
        { id: null, name: null, count: 6 },
      ],
      by_group: [
        { id: 'g1', name: 'Service Desk', count: 30 },
        { id: null, name: null, count: 3 },
      ],
    },
    ...over,
  }
}
