import { describe, expect, it } from 'vitest'
import type { KpiChanges, KpiPeriodMeasures, KpiResponse, MeasureChange } from '@/api/types'
import {
  KPI_MEASURES,
  SLA_OFF_LINE,
  comparisonText,
  filterLine,
  kpiCardModels,
  kpiCsv,
  kpiSearchParams,
  localDay,
  parseKpiSearch,
  presetRange,
  toKpiParams,
  type KpiMeasure,
} from './kpi'

const measure = (key: keyof KpiChanges): KpiMeasure => {
  const m = KPI_MEASURES.find((x) => x.key === key)
  if (!m) throw new Error(`no measure ${key}`)
  return m
}

const ch = (kind: MeasureChange['kind'], state: MeasureChange['state'], value: number | null): MeasureChange => ({
  kind,
  state,
  value,
})

// --- fixtures -----------------------------------------------------------------

const zeroCurrent: KpiPeriodMeasures = {
  created: 0,
  resolved: 0,
  time_to_resolve: { count: 0, median_seconds: null, p90_seconds: null },
  first_response: { count: 0, median_seconds: null },
  sla: {
    tracked: 0,
    met: 0,
    response_met: 0,
    resolution_met: 0,
    met_rate: null,
    response_met_rate: null,
    resolution_met_rate: null,
  },
  reopen: { reopened: 0, rate: null },
}

const noCurrent = (kind: MeasureChange['kind']): MeasureChange => ch(kind, 'no_current', null)

// A period with data: every number here is what the server would have sent.
const response = (): KpiResponse => ({
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
        met_rate: 0.8532,
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
      { date: '2026-10-27', created: 0, resolved: 1 },
    ],
    by_category: [
      { id: 'c1', name: 'Hardware', count: 31 },
      { id: 'c2', name: 'Network', count: 20 },
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
})

const empty = (): KpiResponse => {
  const r = response()
  r.measures.current = zeroCurrent
  r.measures.previous = zeroCurrent
  r.measures.change = {
    created: noCurrent('relative'),
    resolved: noCurrent('relative'),
    time_to_resolve_median: noCurrent('relative'),
    time_to_resolve_p90: noCurrent('relative'),
    first_response_median: noCurrent('relative'),
    sla_met_rate: noCurrent('points'),
    sla_response_met_rate: noCurrent('points'),
    sla_resolution_met_rate: noCurrent('points'),
    reopen_rate: noCurrent('points'),
  }
  r.volume = {
    days: [{ date: '2026-10-26', created: 0, resolved: 0 }],
    by_category: [],
    by_priority: [
      { priority: 'critical', count: 0 },
      { priority: 'high', count: 0 },
      { priority: 'medium', count: 0 },
      { priority: 'low', count: 0 },
    ],
  }
  r.backlog = {
    ...r.backlog,
    total: 0,
    buckets: r.backlog.buckets.map((b) => ({ ...b, count: 0 })),
  }
  r.workload = { ...r.workload, total: 0, by_assignee: [], by_group: [] }
  return r
}

const cardsOf = (r: KpiResponse) => {
  const cards = kpiCardModels(r)
  return (key: string) => {
    const c = cards.find((x) => x.key === key)
    if (!c) throw new Error(`no card ${key}`)
    return c
  }
}

// --- comparisonText ------------------------------------------------------------

describe('comparisonText', () => {
  // The four states, both kinds and all three directions, each with its text,
  // arrow, tone and sentence for a screen reader.
  const cases: Array<{
    name: string
    change: MeasureChange
    measure: keyof KpiChanges
    want: { text: string; direction: string; tone: string; srText: string }
  }> = [
    {
      name: 'relative up, lower is better: bad',
      change: ch('relative', 'change', 0.1667),
      measure: 'time_to_resolve_median',
      want: { text: '▲ 17%', direction: 'up', tone: 'bad', srText: 'up 17% from the previous period' },
    },
    {
      name: 'relative down, lower is better: good',
      change: ch('relative', 'change', -0.0812),
      measure: 'time_to_resolve_median',
      want: { text: '▼ 8%', direction: 'down', tone: 'good', srText: 'down 8% from the previous period' },
    },
    {
      name: 'points up, higher is better: good',
      change: ch('points', 'change', 0.031),
      measure: 'sla_met_rate',
      want: { text: '▲ 3.1 pts', direction: 'up', tone: 'good', srText: 'up 3.1 points from the previous period' },
    },
    {
      name: 'points down, higher is better: bad',
      change: ch('points', 'change', -0.031),
      measure: 'sla_met_rate',
      want: { text: '▼ 3.1 pts', direction: 'down', tone: 'bad', srText: 'down 3.1 points from the previous period' },
    },
    {
      name: 'points up, lower is better: bad',
      change: ch('points', 'change', 0.0506),
      measure: 'reopen_rate',
      want: { text: '▲ 5.1 pts', direction: 'up', tone: 'bad', srText: 'up 5.1 points from the previous period' },
    },
    {
      name: 'relative zero: no change, grey',
      change: ch('relative', 'change', 0),
      measure: 'created',
      want: { text: 'No change', direction: 'flat', tone: 'neutral', srText: 'no change from the previous period' },
    },
    {
      name: 'points zero on a higher-is-better rate: still no change',
      change: ch('points', 'change', 0),
      measure: 'sla_response_met_rate',
      want: { text: 'No change', direction: 'flat', tone: 'neutral', srText: 'no change from the previous period' },
    },
    {
      name: 'neutral measure up: grey, not green',
      change: ch('relative', 'change', 0.1667),
      measure: 'created',
      want: { text: '▲ 17%', direction: 'up', tone: 'neutral', srText: 'up 17% from the previous period' },
    },
    {
      name: 'neutral measure down: grey',
      change: ch('relative', 'change', -0.5),
      measure: 'resolved',
      want: { text: '▼ 50%', direction: 'down', tone: 'neutral', srText: 'down 50% from the previous period' },
    },
    {
      name: 'from zero on a lower-is-better measure: an increase, so bad',
      change: ch('relative', 'from_zero', null),
      measure: 'time_to_resolve_p90',
      want: {
        text: 'Up from 0 in the previous period',
        direction: 'up',
        tone: 'bad',
        srText: 'Up from 0 in the previous period',
      },
    },
    {
      name: 'from zero on a higher-is-better measure: good',
      change: ch('relative', 'from_zero', null),
      measure: 'created',
      want: { text: 'Up from 0 in the previous period', direction: 'up', tone: 'neutral', srText: 'Up from 0 in the previous period' },
    },
    {
      name: 'no previous data: grey',
      change: ch('points', 'no_previous', null),
      measure: 'sla_met_rate',
      want: {
        text: 'No data for the previous period',
        direction: 'none',
        tone: 'neutral',
        srText: 'No data for the previous period',
      },
    },
    {
      name: 'no current data: nothing to say',
      change: ch('relative', 'no_current', null),
      measure: 'time_to_resolve_median',
      want: { text: '', direction: 'none', tone: 'neutral', srText: '' },
    },
  ]

  it.each(cases)('$name', ({ change, measure: key, want }) => {
    expect(comparisonText(change, measure(key))).toEqual(want)
  })

  // Every direction x kind for each `better` value, checked against the rule
  // itself: the tone is good when the direction is the better one, bad when it
  // is against it, and grey for a neutral measure.
  const betters: KpiMeasure['better'][] = ['lower', 'higher', 'neutral']
  const matrix = betters.flatMap((better) =>
    (['relative', 'points'] as const).flatMap((kind) =>
      [1, -1].map((sign) => ({ better, kind, sign }))
    )
  )
  it.each(matrix)('tone for better=$better, $kind, sign $sign', ({ better, kind, sign }) => {
    const m: KpiMeasure = { key: 'created', label: 'x', unit: 'count', better }
    const got = comparisonText(ch(kind, 'change', 0.5 * sign), m)
    const up = sign > 0
    const tone = better === 'neutral' ? 'neutral' : better === 'higher' === up ? 'good' : 'bad'
    expect(got.tone).toBe(tone)
    expect(got.direction).toBe(up ? 'up' : 'down')
    expect(got.text.startsWith(up ? '▲' : '▼')).toBe(true)
  })
})

describe('KPI_MEASURES', () => {
  it('lists the nine compared measures with the direction that counts as better', () => {
    expect(KPI_MEASURES.map((m) => [m.key, m.better])).toEqual([
      ['created', 'neutral'],
      ['resolved', 'neutral'],
      ['time_to_resolve_median', 'lower'],
      ['time_to_resolve_p90', 'lower'],
      ['first_response_median', 'lower'],
      ['sla_met_rate', 'higher'],
      ['sla_response_met_rate', 'higher'],
      ['sla_resolution_met_rate', 'higher'],
      ['reopen_rate', 'lower'],
    ])
  })
})

// --- filterLine ----------------------------------------------------------------

describe('filterLine', () => {
  const none = { category_id: null, priority: [], assignee_group_id: null }
  const cases: Array<{
    name: string
    filters: KpiResponse['filters']
    names: Parameters<typeof filterLine>[1]
    want: string
  }> = [
    {
      name: 'nothing chosen',
      filters: none,
      names: {},
      want: 'All categories · All priorities · All groups',
    },
    {
      name: 'the chosen names, priorities in the order the server sent them',
      filters: { category_id: 'c1', priority: ['critical', 'high'], assignee_group_id: 'g1' },
      names: { category: 'Hardware', group: 'Service Desk' },
      want: 'Hardware · Critical, High · Service Desk',
    },
    {
      name: 'a chosen id whose name is not known reads as a selection, not as its id',
      filters: { category_id: 'c1', priority: ['low'], assignee_group_id: 'g1' },
      names: {},
      want: 'Selected category · Low · Selected group',
    },
  ]

  it.each(cases)('$name', ({ filters, names, want }) => {
    expect(filterLine(filters, names)).toBe(want)
  })
})

// --- search and parameters -------------------------------------------------------

describe('presetRange', () => {
  // A fixed "today" (Tuesday 24 November 2026). 30 days is the server's
  // own example: 26 Oct to 24 Nov.
  const cases: Array<[number, string, { from: string; to: string }]> = [
    [7, '2026-11-24', { from: '2026-11-18', to: '2026-11-24' }],
    [30, '2026-11-24', { from: '2026-10-26', to: '2026-11-24' }],
    [90, '2026-11-24', { from: '2026-08-27', to: '2026-11-24' }],
    [1, '2026-03-01', { from: '2026-03-01', to: '2026-03-01' }],
    [7, '2028-03-03', { from: '2028-02-26', to: '2028-03-03' }],
  ]
  it.each(cases)('%i days ending %s', (days, today, want) => {
    expect(presetRange(days, today)).toEqual(want)
  })
})

describe('localDay', () => {
  it('is the calendar date on the local clock, zero padded', () => {
    expect(localDay(new Date(2026, 10, 24, 23, 30))).toBe('2026-11-24')
    expect(localDay(new Date(2026, 0, 2, 0, 5))).toBe('2026-01-02')
  })
})

describe('parseKpiSearch', () => {
  const cases: Array<{ name: string; query: string; want: ReturnType<typeof parseKpiSearch> }> = [
    {
      name: 'nothing is the last 30 days with no filters',
      query: '',
      want: { range: { kind: 'preset', days: 30 }, categoryId: null, priorities: [], groupId: null },
    },
    {
      name: 'range=7 and range=90',
      query: 'range=7',
      want: { range: { kind: 'preset', days: 7 }, categoryId: null, priorities: [], groupId: null },
    },
    {
      name: 'an unknown range is 30',
      query: 'range=45',
      want: { range: { kind: 'preset', days: 30 }, categoryId: null, priorities: [], groupId: null },
    },
    {
      name: 'a custom range takes from and to as written',
      query: 'from=2026-11-01&to=2026-11-03',
      want: { range: { kind: 'custom', from: '2026-11-01', to: '2026-11-03' }, categoryId: null, priorities: [], groupId: null },
    },
    {
      name: 'one date alone is a custom range the server will refuse',
      query: 'from=2026-11-01',
      want: { range: { kind: 'custom', from: '2026-11-01', to: null }, categoryId: null, priorities: [], groupId: null },
    },
    {
      name: 'empty dates are no custom range',
      query: 'from=&to=&range=90',
      want: { range: { kind: 'preset', days: 90 }, categoryId: null, priorities: [], groupId: null },
    },
    {
      name: 'every filter; priorities deduplicated, unknown ones dropped, in the fixed order',
      query: 'category_id=c1&priority=low&priority=high&priority=urgent&priority=high&assignee_group_id=g1',
      want: {
        range: { kind: 'preset', days: 30 },
        categoryId: 'c1',
        priorities: ['high', 'low'],
        groupId: 'g1',
      },
    },
    {
      name: 'an empty id is no filter',
      query: 'category_id=&assignee_group_id=',
      want: { range: { kind: 'preset', days: 30 }, categoryId: null, priorities: [], groupId: null },
    },
  ]

  it.each(cases)('$name', ({ query, want }) => {
    expect(parseKpiSearch(new URLSearchParams(query))).toEqual(want)
  })

  // The address is the state: writing a parsed search and reading it back gives
  // the same state, for every shape above.
  it.each(cases)('round trips: $name', ({ query }) => {
    const once = parseKpiSearch(new URLSearchParams(query))
    expect(parseKpiSearch(kpiSearchParams(once))).toEqual(once)
  })
})

describe('kpiSearchParams', () => {
  it('writes a preset as range and a custom range as from and to', () => {
    expect(kpiSearchParams(parseKpiSearch(new URLSearchParams('range=90'))).toString()).toBe('range=90')
    expect(kpiSearchParams(parseKpiSearch(new URLSearchParams('from=2026-11-01&to=2026-11-03'))).toString()).toBe(
      'from=2026-11-01&to=2026-11-03'
    )
  })
})

describe('toKpiParams', () => {
  const today = '2026-11-24'
  const tz = 'Europe/London'
  const cases: Array<{ name: string; query: string; want: Record<string, unknown> }> = [
    {
      name: 'a preset is its dates ending today, and always the zone',
      query: 'range=7',
      want: { tz, from: '2026-11-18', to: '2026-11-24' },
    },
    {
      name: 'a custom range is sent as written',
      query: 'from=2026-11-01&to=2026-11-03',
      want: { tz, from: '2026-11-01', to: '2026-11-03' },
    },
    {
      name: 'one custom date is sent alone, so the server names what is missing',
      query: 'from=2026-11-01',
      want: { tz, from: '2026-11-01' },
    },
    {
      name: 'every filter',
      query: 'range=30&category_id=c1&priority=critical&priority=low&assignee_group_id=g1',
      want: {
        tz,
        from: '2026-10-26',
        to: '2026-11-24',
        category_id: 'c1',
        priority: ['critical', 'low'],
        assignee_group_id: 'g1',
      },
    },
  ]

  it.each(cases)('$name', ({ query, want }) => {
    expect(toKpiParams(parseKpiSearch(new URLSearchParams(query)), tz, today)).toEqual(want)
  })
})

// --- kpiCsv --------------------------------------------------------------------

describe('kpiCsv', () => {
  const names = { category: undefined, group: undefined }
  const lines = (csv: string) => csv.split('\r\n')

  it('starts with a byte order mark and ends every line with CRLF', () => {
    const csv = kpiCsv(response(), names)
    expect(csv.startsWith('﻿')).toBe(true)
    expect(csv.endsWith('\r\n')).toBe(true)
    expect(csv.includes('\n') && !csv.replace(/\r\n/g, '').includes('\n')).toBe(true)
  })

  it('has the header, then the context rows first', () => {
    const l = lines(kpiCsv(response(), names).slice(1))
    expect(l[0]).toBe('measure,key,label,current,previous,change,unit')
    expect(l.slice(1, 9)).toEqual([
      'period_from,,2026-10-26,,,,',
      'period_to,,2026-11-24,,,,',
      'previous_from,,2026-09-26,,,,',
      'previous_to,,2026-10-25,,,,',
      'tz,,Europe/London,,,,',
      'filter_category,,,,,,',
      'filter_priority,,,,,,',
      'filter_group,,,,,,',
    ].map((row) => row))
  })

  it('writes the period measures with both values and the server change in the nine compared rows', () => {
    const l = lines(kpiCsv(response(), names))
    expect(l).toContain('created,,Created,84,72,0.1667,count')
    expect(l).toContain('resolved,,Resolved,79,70,0.1286,count')
    expect(l).toContain('time_to_resolve_median,,Time to resolve (median),52200,55000,-0.0509,seconds')
    expect(l).toContain('time_to_resolve_p90,,Time to resolve (90th percentile),345600,300000,0.152,seconds')
    expect(l).toContain('first_response_median,,First response (median),3900,4200,-0.0714,seconds')
    expect(l).toContain('sla_met_rate,,SLA met,0.8532,0.8,0.0532,rate')
    expect(l).toContain('sla_response_met_rate,,SLA response met,0.9344,0.9091,0.0253,rate')
    expect(l).toContain('sla_resolution_met_rate,,SLA resolution met,0.8852,0.8727,0.0125,rate')
    expect(l).toContain('reopen_rate,,Reopen rate,0.0506,0,0.0506,rate')
  })

  it('leaves the change empty for a measure that is not compared', () => {
    const l = lines(kpiCsv(response(), names))
    expect(l).toContain('time_to_resolve_count,,Time to resolve (count),79,70,,count')
    expect(l).toContain('first_response_count,,First response (count),61,55,,count')
    expect(l).toContain('sla_tracked,,SLA tracked,61,55,,count')
    expect(l).toContain('reopened,,Reopened,4,0,,count')
  })

  it('writes an empty cell for a null value and an empty change for a null change', () => {
    const r = response()
    r.measures.current.time_to_resolve = { count: 0, median_seconds: null, p90_seconds: null }
    r.measures.change.time_to_resolve_median = noCurrent('relative')
    const l = lines(kpiCsv(r, names))
    expect(l).toContain('time_to_resolve_median,,Time to resolve (median),,55000,,seconds')
  })

  it('writes every breakdown and workload row, the null rows with their names', () => {
    const l = lines(kpiCsv(response(), names))
    expect(l).toContain('created_by_day,2026-10-26,,3,,,count')
    expect(l).toContain('resolved_by_day,2026-10-27,,1,,,count')
    expect(l).toContain('created_by_category,c1,Hardware,31,,,count')
    expect(l).toContain('created_by_category,c2,Network,20,,,count')
    expect(l).toContain('created_by_priority,critical,Critical,2,,,count')
    expect(l).toContain('created_by_priority,low,Low,13,,,count')
    expect(l).toContain('backlog,0_2,0–2 days,17,,,count')
    expect(l).toContain('backlog,over_30,Over 30 days,4,,,count')
    expect(l).toContain('workload_person,u1,Ada Lovelace,9,,,count')
    expect(l).toContain('workload_person,,Nobody,6,,,count')
    expect(l).toContain('workload_group,g1,Service Desk,30,,,count')
    expect(l).toContain('workload_group,,No group,3,,,count')
  })

  it('quotes a field with a comma, a quote or a line break, doubling the quotes', () => {
    const r = response()
    r.volume.by_category = [
      { id: 'c1', name: 'Hardware, "Core"', count: 1 },
      { id: 'c2', name: 'a\nb', count: 1 },
    ]
    const csv = kpiCsv(r, names)
    expect(csv).toContain('created_by_category,c1,"Hardware, ""Core""",1,,,count\r\n')
    expect(csv).toContain('created_by_category,c2,"a\nb",1,,,count\r\n')
  })

  // A label that a spreadsheet would read as a formula is text: it gets a
  // leading apostrophe. Numbers never do, so the negative changes are untouched.
  it.each([
    ['=', '=1+1'],
    ['+', '+2'],
    ['-', '-3'],
    ['@', '@4'],
    ['TAB', '\t5'],
    ['CR', '\r6'],
  ])('prefixes a label that starts with %s', (_name, label) => {
    const r = response()
    r.volume.by_category = [{ id: 'c9', name: label, count: 1 }]
    const csv = kpiCsv(r, names)
    const prefixed = `'${label}`
    const cell = /[",\r\n]/.test(prefixed) ? `"${prefixed.replace(/"/g, '""')}"` : prefixed
    expect(csv).toContain(`created_by_category,c9,${cell},1,,,count\r\n`)
  })

  it('leaves a negative change as a number', () => {
    expect(kpiCsv(response(), names)).toContain('time_to_resolve_median,,Time to resolve (median),52200,55000,-0.0509,seconds')
  })

  it('names the chosen filters in the context rows', () => {
    const r = response()
    r.filters = { category_id: 'c1', priority: ['critical', 'high'], assignee_group_id: 'g1' }
    const l = lines(kpiCsv(r, { category: 'Hardware', group: 'Service Desk' }))
    expect(l).toContain('filter_category,,Hardware,,,,')
    expect(l).toContain('filter_priority,,"Critical, High",,,,')
    expect(l).toContain('filter_group,,Service Desk,,,,')
  })
})

// --- kpiCardModels -------------------------------------------------------------

describe('kpiCardModels', () => {
  it('has the seven cards in order', () => {
    expect(kpiCardModels(response()).map((c) => [c.key, c.title])).toEqual([
      ['time_to_resolve', 'Time to resolve'],
      ['first_response', 'First response'],
      ['sla', 'SLA attainment'],
      ['reopen', 'Reopen rate'],
      ['volume', 'Volume'],
      ['backlog', 'Backlog aging'],
      ['workload', 'Workload'],
    ])
  })

  it('shows the time to resolve median and 90th percentile, with the count', () => {
    const c = cardsOf(response())('time_to_resolve')
    expect(c.figures.map((f) => [f.label, f.value])).toEqual([
      ['Median', '14 h 30 min'],
      ['90th percentile', '4 d'],
    ])
    expect(c.figures[0].comparison?.text).toBe('▼ 5%')
    expect(c.figures[0].comparison).toEqual(
      comparisonText(response().measures.change.time_to_resolve_median, measure('time_to_resolve_median'))
    )
    expect(c.lines).toEqual(['79 tickets resolved'])
    expect(c.emptyText).toBeUndefined()
  })

  it('shows the first response median, with the count', () => {
    const c = cardsOf(response())('first_response')
    expect(c.figures.map((f) => [f.label, f.value])).toEqual([['Median', '1 h 5 min']])
    expect(c.lines).toEqual(['61 tickets with an SLA first response'])
  })

  it('shows the SLA rates as percentages, each with its own comparison', () => {
    const c = cardsOf(response())('sla')
    expect(c.figures.map((f) => [f.label, f.value])).toEqual([
      ['Met', '85.3%'],
      ['Response met', '93.4%'],
      ['Resolution met', '88.5%'],
    ])
    expect(c.figures[0].comparison?.text).toBe('▲ 5.3 pts')
    expect(c.lines).toEqual(['61 tickets tracked'])
  })

  it('shows the reopen rate and what it is a share of', () => {
    const c = cardsOf(response())('reopen')
    expect(c.figures.map((f) => [f.label, f.value])).toEqual([['Reopened', '5.1%']])
    expect(c.lines).toEqual(['4 of 79 resolved tickets had been reopened'])
  })

  it('shows created and resolved with their comparisons, and the two bar charts', () => {
    const c = cardsOf(response())('volume')
    expect(c.figures.map((f) => [f.label, f.value])).toEqual([
      ['Created', '84'],
      ['Resolved', '79'],
    ])
    expect(c.days).toEqual(response().volume.days)
    expect(c.charts.map((x) => x.key)).toEqual(['priority', 'category'])
    expect(c.charts[0].rows.map((r) => [r.label, r.value])).toEqual([
      ['Critical', 2],
      ['High', 9],
      ['Medium', 60],
      ['Low', 13],
    ])
    expect(c.charts[1].rows.map((r) => [r.key, r.label, r.value])).toEqual([
      ['c1', 'Hardware', 31],
      ['c2', 'Network', 20],
    ])
  })

  it('shows the open total and the four age buckets, with no comparison', () => {
    const c = cardsOf(response())('backlog')
    expect(c.figures.map((f) => [f.label, f.value])).toEqual([['Open now', '42']])
    expect(c.figures[0].comparison).toBeUndefined()
    expect(c.charts[0].rows.map((r) => [r.key, r.label, r.value])).toEqual([
      ['0_2', '0–2 days', 17],
      ['3_7', '3–7 days', 11],
      ['8_30', '8–30 days', 10],
      ['over_30', 'Over 30 days', 4],
    ])
  })

  it('shows workload by person and by group, with the null rows named', () => {
    const c = cardsOf(response())('workload')
    expect(c.figures.map((f) => [f.label, f.value])).toEqual([['Open now', '42']])
    expect(c.charts.map((x) => x.key)).toEqual(['person', 'group'])
    expect(c.charts[0].rows.map((r) => [r.key, r.label, r.value])).toEqual([
      ['u1', 'Ada Lovelace', 9],
      ['nobody', 'Nobody', 6],
    ])
    expect(c.charts[1].rows.map((r) => [r.key, r.label, r.value])).toEqual([
      ['g1', 'Service Desk', 30],
      ['no-group', 'No group', 3],
    ])
  })

  it('drops a comparison whose text is empty (no current data)', () => {
    const r = response()
    r.measures.change.time_to_resolve_median = noCurrent('relative')
    expect(cardsOf(r)('time_to_resolve').figures[0].comparison).toBeUndefined()
  })

  describe('an empty period says what it lacks, with no figures', () => {
    const cases: Array<{ card: string; text: string }> = [
      { card: 'time_to_resolve', text: 'No tickets were resolved in this period.' },
      { card: 'first_response', text: 'No ticket resolved in this period had an SLA first response.' },
      { card: 'sla', text: 'No ticket resolved in this period was tracked by an SLA.' },
      { card: 'reopen', text: 'No tickets were resolved in this period.' },
      { card: 'volume', text: 'No tickets were created or resolved in this period.' },
      { card: 'backlog', text: 'No open tickets.' },
      { card: 'workload', text: 'No open tickets.' },
    ]
    it.each(cases)('$card', ({ card, text }) => {
      const c = cardsOf(empty())(card)
      expect(c.emptyText).toBe(text)
      expect(c.figures).toEqual([])
      expect(c.charts).toEqual([])
      expect(c.lines).toEqual([])
    })
  })

  describe('SLA tracking off', () => {
    const off = (): KpiResponse => ({ ...response(), sla_enabled: false })

    it('adds the one muted line to first response and SLA attainment only', () => {
      const cards = kpiCardModels(off())
      const withLine = cards.filter((c) => c.lines.includes(SLA_OFF_LINE)).map((c) => c.key)
      expect(withLine).toEqual(['first_response', 'sla'])
      expect(SLA_OFF_LINE).toBe(
        'SLA tracking is off, so new tickets are not timed. These figures cover tickets timed while it was on.'
      )
    })

    it('still adds the line when the SLA card is empty', () => {
      const r = off()
      r.measures.current.sla = zeroCurrent.sla
      const c = cardsOf(r)('sla')
      expect(c.emptyText).toBe('No ticket resolved in this period was tracked by an SLA.')
      expect(c.lines).toEqual([SLA_OFF_LINE])
    })

    it('does not add it with SLA tracking on', () => {
      expect(kpiCardModels(response()).some((c) => c.lines.includes(SLA_OFF_LINE))).toBe(false)
    })
  })
})
