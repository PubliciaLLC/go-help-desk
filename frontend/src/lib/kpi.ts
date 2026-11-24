import type { KpiParams } from '@/api/reports'
import type {
  KpiChanges,
  KpiFilters,
  KpiPeriodMeasures,
  KpiResponse,
  MeasureChange,
  Priority,
  VolumeDay,
} from '@/api/types'
import { fmtDuration } from './format'

// The Reports page as data (docs/DESIGN.md, "KPI reports"). Every number comes
// from the server. This module chooses words, arrows, tones and the order of
// rows, and formats what the server sent. It does no arithmetic on the figures
// beyond scaling a fraction to a percentage (x 100) for display.

// --- measures and comparisons -----------------------------------------------

export type KpiMeasureKey = keyof KpiChanges

export interface KpiMeasure {
  key: KpiMeasureKey
  label: string
  unit: 'count' | 'seconds' | 'rate'
  better: 'lower' | 'higher' | 'neutral'
}

// The nine measures a card compares, with the direction that counts as better.
export const KPI_MEASURES: KpiMeasure[] = [
  { key: 'created', label: 'Created', unit: 'count', better: 'neutral' },
  { key: 'resolved', label: 'Resolved', unit: 'count', better: 'neutral' },
  { key: 'time_to_resolve_median', label: 'Time to resolve (median)', unit: 'seconds', better: 'lower' },
  { key: 'time_to_resolve_p90', label: 'Time to resolve (90th percentile)', unit: 'seconds', better: 'lower' },
  { key: 'first_response_median', label: 'First response (median)', unit: 'seconds', better: 'lower' },
  { key: 'sla_met_rate', label: 'SLA met', unit: 'rate', better: 'higher' },
  { key: 'sla_response_met_rate', label: 'SLA response met', unit: 'rate', better: 'higher' },
  { key: 'sla_resolution_met_rate', label: 'SLA resolution met', unit: 'rate', better: 'higher' },
  { key: 'reopen_rate', label: 'Reopen rate', unit: 'rate', better: 'lower' },
]

const MEASURE = Object.fromEntries(KPI_MEASURES.map((m) => [m.key, m])) as Record<KpiMeasureKey, KpiMeasure>

export type Tone = 'good' | 'bad' | 'neutral'

export interface ComparisonText {
  text: string
  direction: 'up' | 'down' | 'flat' | 'none'
  tone: Tone
  srText: string
}

const NO_CURRENT: ComparisonText = { text: '', direction: 'none', tone: 'neutral', srText: '' }

// The line under a compared figure, from the server's change for it. The state
// decides the words; the sign of the value decides the arrow; the measure's
// `better` decides the colour.
export function comparisonText(change: MeasureChange, measure: KpiMeasure): ComparisonText {
  const toneFor = (up: boolean): Tone => {
    if (measure.better === 'neutral') return 'neutral'
    return (measure.better === 'higher') === up ? 'good' : 'bad'
  }
  switch (change.state) {
    case 'from_zero': {
      const text = 'Up from 0 in the previous period'
      return { text, direction: 'up', tone: toneFor(true), srText: text }
    }
    case 'no_previous': {
      const text = 'No data for the previous period'
      return { text, direction: 'none', tone: 'neutral', srText: text }
    }
    case 'change':
      break
    default:
      return { ...NO_CURRENT }
  }
  const v = change.value
  if (v === null) return { ...NO_CURRENT }
  if (v === 0) {
    return { text: 'No change', direction: 'flat', tone: 'neutral', srText: 'no change from the previous period' }
  }
  const up = v > 0
  const points = change.kind === 'points'
  const num = (Math.abs(v) * 100).toFixed(points ? 1 : 0)
  return {
    text: `${up ? '▲' : '▼'} ${num}${points ? ' pts' : '%'}`,
    direction: up ? 'up' : 'down',
    tone: toneFor(up),
    srText: `${up ? 'up' : 'down'} ${num}${points ? ' points' : '%'} from the previous period`,
  }
}

// --- search state and the request ------------------------------------------

export type KpiPresetDays = 7 | 30 | 90

// A preset is the last N days ending today. A custom range keeps what the
// address said, so a half-typed range reaches the server and its message shows.
export type KpiRange =
  | { kind: 'preset'; days: KpiPresetDays }
  | { kind: 'custom'; from: string | null; to: string | null }

export interface KpiSearch {
  range: KpiRange
  categoryId: string | null
  priorities: Priority[]
  groupId: string | null
}

const PRIORITY_ORDER: Priority[] = ['critical', 'high', 'medium', 'low']
const PRIORITY_LABEL: Record<Priority, string> = { critical: 'Critical', high: 'High', medium: 'Medium', low: 'Low' }

function presetDays(v: string | null): KpiPresetDays {
  if (v === '7') return 7
  if (v === '90') return 90
  return 30
}

// The address is the state: range=7|30|90 (default 30), or from and to for a
// custom range, then category_id, repeated priority and assignee_group_id.
export function parseKpiSearch(p: URLSearchParams): KpiSearch {
  const from = p.get('from') || null
  const to = p.get('to') || null
  const given = p.getAll('priority')
  return {
    range: from || to ? { kind: 'custom', from, to } : { kind: 'preset', days: presetDays(p.get('range')) },
    categoryId: p.get('category_id') || null,
    priorities: PRIORITY_ORDER.filter((x) => given.includes(x)),
    groupId: p.get('assignee_group_id') || null,
  }
}

export function kpiSearchParams(s: KpiSearch): URLSearchParams {
  const p = new URLSearchParams()
  if (s.range.kind === 'preset') {
    p.set('range', String(s.range.days))
  } else {
    if (s.range.from) p.set('from', s.range.from)
    if (s.range.to) p.set('to', s.range.to)
  }
  if (s.categoryId) p.set('category_id', s.categoryId)
  for (const x of s.priorities) p.append('priority', x)
  if (s.groupId) p.set('assignee_group_id', s.groupId)
  return p
}

function addDays(day: string, n: number): string {
  const [y, m, d] = day.split('-').map(Number)
  return new Date(Date.UTC(y, m - 1, d + n)).toISOString().slice(0, 10)
}

// The dates of the last `days` days ending `today` (YYYY-MM-DD), both inclusive.
export function presetRange(days: number, today: string): { from: string; to: string } {
  return { from: addDays(today, 1 - days), to: today }
}

// The browser's calendar date, which is the date in the zone it sends as tz.
export function localDay(now: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`
}

// The router hands the address over as a record: range=30 arrives as the number
// 30 and a lone priority as a string, and priority=high&priority=low as an array
// (or, with the router's own encoding, a JSON list). Anything else is ignored.
export function kpiSearchFromRecord(rec: Record<string, unknown>): KpiSearch {
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries(rec)) {
    const items = Array.isArray(v) ? v : [v]
    for (const item of items) {
      if (typeof item === 'string' || typeof item === 'number') p.append(k, String(item))
    }
  }
  return parseKpiSearch(p)
}

// A search state as the record the router writes into the address: a repeated
// priority is a list, everything else a string.
export function kpiSearchToRecord(s: KpiSearch): Record<string, string | string[]> {
  const out: Record<string, string | string[]> = {}
  const p = kpiSearchParams(s)
  for (const k of new Set(p.keys())) {
    const all = p.getAll(k)
    out[k] = k === 'priority' ? all : all[0]
  }
  return out
}

// The request for a search state. `today` is the day in `tz`, from localDay.
export function toKpiParams(s: KpiSearch, tz: string, today: string): KpiParams {
  const dates =
    s.range.kind === 'preset'
      ? presetRange(s.range.days, today)
      : { from: s.range.from ?? undefined, to: s.range.to ?? undefined }
  return {
    tz,
    ...dates,
    category_id: s.categoryId ?? undefined,
    priority: s.priorities.length ? s.priorities : undefined,
    assignee_group_id: s.groupId ?? undefined,
  }
}

// --- filter line ---------------------------------------------------------------

// The names of the chosen category and group, from the lookups the list uses.
export interface KpiNames {
  category?: string
  group?: string
}

export function filterLine(filters: KpiFilters, names: KpiNames): string {
  return [
    filters.category_id ? (names.category ?? 'Selected category') : 'All categories',
    filters.priority.length ? filters.priority.map((p) => PRIORITY_LABEL[p]).join(', ') : 'All priorities',
    filters.assignee_group_id ? (names.group ?? 'Selected group') : 'All groups',
  ].join(' · ')
}

// --- period line and file names -----------------------------------------------

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

// "2026-10-26" as "26 Oct", read as the parts it is made of (a calendar day in
// the viewer's zone, not an instant).
function dayMonth(ymd: string): string {
  const [, m, d] = ymd.split('-').map(Number)
  return `${d} ${MONTHS[m - 1]}`
}

const yearOf = (ymd: string): string => ymd.slice(0, 4)

// A range as "26 Oct – 24 Nov 2026". The year closes the range when both ends
// are in it; across New Year each end says its own. `closingYear` is false for
// a range whose year the sentence already gave.
function rangeText(from: string, to: string, closingYear = true): string {
  if (from === to) return `${dayMonth(to)} ${yearOf(to)}`
  if (yearOf(from) !== yearOf(to)) return `${dayMonth(from)} ${yearOf(from)} – ${dayMonth(to)} ${yearOf(to)}`
  return `${dayMonth(from)} – ${dayMonth(to)}${closingYear ? ` ${yearOf(to)}` : ''}`
}

// The period alone: "26 Oct – 24 Nov 2026".
export function periodRange(resp: KpiResponse): string {
  return rangeText(resp.period.from, resp.period.to)
}

// "26 Oct – 24 Nov 2026, compared with 26 Sep – 25 Oct". The comparison
// leaves its year out when it is the current period's single year.
export function periodLine(resp: KpiResponse): string {
  const cur = resp.period
  const prev = resp.previous
  const sameYear = yearOf(cur.from) === yearOf(cur.to) && yearOf(prev.from) === yearOf(cur.to) && yearOf(prev.to) === yearOf(cur.to)
  const prevText = prev.from === prev.to && sameYear ? dayMonth(prev.to) : rangeText(prev.from, prev.to, !sameYear)
  return `${periodRange(resp)}, compared with ${prevText}`
}

// kpis-<from>-to-<to>.csv or .png for the whole board; kpi-<card key>-<from>-to-<to>.png
// for one card.
export function kpiFileName(resp: KpiResponse, ext: 'png' | 'csv', card?: KpiCardKey): string {
  const span = `${resp.period.from}-to-${resp.period.to}`
  return card ? `kpi-${card}-${span}.${ext}` : `kpis-${span}.${ext}`
}

// --- CSV -----------------------------------------------------------------------

const BOM = '﻿'
const CSV_HEADER = 'measure,key,label,current,previous,change,unit'
const BUCKET_LABEL: Record<string, string> = {
  '0_2': '0–2 days',
  '3_7': '3–7 days',
  '8_30': '8–30 days',
  over_30: 'Over 30 days',
}

// A text cell a spreadsheet would read as a formula is text: it gets a leading
// apostrophe. Numbers never go through this, so a negative change stays a number.
function formulaSafe(s: string): string {
  return /^[=+\-@\t\r]/.test(s) ? `'${s}` : s
}

// RFC 4180: a field with a comma, a quote or a line break is quoted, and its
// quotes doubled.
function quote(s: string): string {
  return /[",\r\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s
}

function csvLine(measure: string, key: string, label: string, current: string, previous: string, change: string, unit: string): string {
  return [measure, formulaSafe(key), formulaSafe(label), current, previous, change, unit].map(quote).join(',')
}

const num = (n: number | null | undefined): string => (n === null || n === undefined ? '' : String(n))

// The period measures, in the order the CSV lists them. `change` names the
// compared measure whose server change goes in the change column.
const PERIOD_ROWS: Array<{
  name: string
  label: string
  unit: string
  get: (m: KpiPeriodMeasures) => number | null
  change?: KpiMeasureKey
}> = [
  { name: 'created', label: 'Created', unit: 'count', get: (m) => m.created, change: 'created' },
  { name: 'resolved', label: 'Resolved', unit: 'count', get: (m) => m.resolved, change: 'resolved' },
  { name: 'time_to_resolve_median', label: 'Time to resolve (median)', unit: 'seconds', get: (m) => m.time_to_resolve.median_seconds, change: 'time_to_resolve_median' },
  { name: 'time_to_resolve_p90', label: 'Time to resolve (90th percentile)', unit: 'seconds', get: (m) => m.time_to_resolve.p90_seconds, change: 'time_to_resolve_p90' },
  { name: 'time_to_resolve_count', label: 'Time to resolve (count)', unit: 'count', get: (m) => m.time_to_resolve.count },
  { name: 'first_response_median', label: 'First response (median)', unit: 'seconds', get: (m) => m.first_response.median_seconds, change: 'first_response_median' },
  { name: 'first_response_count', label: 'First response (count)', unit: 'count', get: (m) => m.first_response.count },
  { name: 'sla_tracked', label: 'SLA tracked', unit: 'count', get: (m) => m.sla.tracked },
  { name: 'sla_met', label: 'SLA met (count)', unit: 'count', get: (m) => m.sla.met },
  { name: 'sla_response_met', label: 'SLA response met (count)', unit: 'count', get: (m) => m.sla.response_met },
  { name: 'sla_resolution_met', label: 'SLA resolution met (count)', unit: 'count', get: (m) => m.sla.resolution_met },
  { name: 'sla_met_rate', label: 'SLA met', unit: 'rate', get: (m) => m.sla.met_rate, change: 'sla_met_rate' },
  { name: 'sla_response_met_rate', label: 'SLA response met', unit: 'rate', get: (m) => m.sla.response_met_rate, change: 'sla_response_met_rate' },
  { name: 'sla_resolution_met_rate', label: 'SLA resolution met', unit: 'rate', get: (m) => m.sla.resolution_met_rate, change: 'sla_resolution_met_rate' },
  { name: 'reopened', label: 'Reopened', unit: 'count', get: (m) => m.reopen.reopened },
  { name: 'reopen_rate', label: 'Reopen rate', unit: 'rate', get: (m) => m.reopen.rate, change: 'reopen_rate' },
]

// The CSV the page offers: context first, then every period measure with both
// periods, then the breakdowns and workload rows of the current period. Built
// from the response on screen, RFC 4180, CRLF, with a byte order mark so
// spreadsheet programs read the names as UTF-8.
export function kpiCsv(resp: KpiResponse, names: KpiNames): string {
  const cur = resp.measures.current
  const prev = resp.measures.previous
  const lines: string[] = [CSV_HEADER]

  const context: Array<[string, string]> = [
    ['period_from', resp.period.from],
    ['period_to', resp.period.to],
    ['previous_from', resp.previous.from],
    ['previous_to', resp.previous.to],
    ['tz', resp.tz],
    ['filter_category', names.category ?? resp.filters.category_id ?? ''],
    ['filter_priority', resp.filters.priority.map((p) => PRIORITY_LABEL[p]).join(', ')],
    ['filter_group', names.group ?? resp.filters.assignee_group_id ?? ''],
  ]
  for (const [name, value] of context) lines.push(csvLine(name, '', value, '', '', '', ''))

  for (const r of PERIOD_ROWS) {
    const change = r.change ? num(resp.measures.change[r.change].value) : ''
    lines.push(csvLine(r.name, '', r.label, num(r.get(cur)), num(r.get(prev)), change, r.unit))
  }

  for (const d of resp.volume.days) {
    lines.push(csvLine('created_by_day', d.date, '', num(d.created), '', '', 'count'))
  }
  for (const d of resp.volume.days) {
    lines.push(csvLine('resolved_by_day', d.date, '', num(d.resolved), '', '', 'count'))
  }
  for (const c of resp.volume.by_category) {
    lines.push(csvLine('created_by_category', c.id, c.name, num(c.count), '', '', 'count'))
  }
  for (const p of resp.volume.by_priority) {
    lines.push(csvLine('created_by_priority', p.priority, PRIORITY_LABEL[p.priority], num(p.count), '', '', 'count'))
  }
  for (const b of resp.backlog.buckets) {
    lines.push(csvLine('backlog', b.key, BUCKET_LABEL[b.key] ?? b.key, num(b.count), '', '', 'count'))
  }
  for (const r of resp.workload.by_assignee) {
    lines.push(csvLine('workload_person', r.id ?? '', r.name ?? 'Nobody', num(r.count), '', '', 'count'))
  }
  for (const r of resp.workload.by_group) {
    lines.push(csvLine('workload_group', r.id ?? '', r.name ?? 'No group', num(r.count), '', '', 'count'))
  }

  return BOM + lines.map((l) => `${l}\r\n`).join('')
}

// --- cards ---------------------------------------------------------------------

export const SLA_OFF_LINE =
  'SLA tracking is off, so new tickets are not timed. These figures cover tickets timed while it was on.'

export type KpiCardKey = 'time_to_resolve' | 'first_response' | 'sla' | 'reopen' | 'volume' | 'backlog' | 'workload'

// The cards in the order the page shows them, and their titles: the page draws
// a titled box for each before any data has arrived.
export const KPI_CARD_ORDER: KpiCardKey[] = ['time_to_resolve', 'first_response', 'sla', 'reopen', 'volume', 'backlog', 'workload']

export const KPI_CARD_TITLES: Record<KpiCardKey, string> = {
  time_to_resolve: 'Time to resolve',
  first_response: 'First response',
  sla: 'SLA attainment',
  reopen: 'Reopen rate',
  volume: 'Volume',
  backlog: 'Backlog aging',
  workload: 'Workload',
}

export interface KpiFigure {
  label: string
  value: string
  comparison?: ComparisonText
  tone: Tone
}

export interface KpiChartRow {
  key: string
  label: string
  value: number
}

export interface KpiChart {
  key: string
  title: string
  rows: KpiChartRow[]
}

export interface KpiCardModel {
  key: KpiCardKey
  title: string
  figures: KpiFigure[]
  lines: string[]
  // Set when the period has nothing to show; the card then shows this instead
  // of figures and charts.
  emptyText?: string
  charts: KpiChart[]
  // The daily series of the volume card.
  days?: VolumeDay[]
}

const pct = (r: number): string => `${(r * 100).toFixed(1)}%`
const count = (n: number): string => String(n)
const plural = (n: number, word: string): string => `${n} ${n === 1 ? word : `${word}s`}`

// The card models, in the order the page shows them. The card component draws
// each one; the CSV and the image export never read these models.
export function kpiCardModels(resp: KpiResponse): KpiCardModel[] {
  const cur = resp.measures.current
  const changes = resp.measures.change
  const slaLine = resp.sla_enabled ? [] : [SLA_OFF_LINE]

  // A figure with its server change, or with none; null when there is no number.
  const fig = (label: string, value: number | null, fmt: (n: number) => string, key?: KpiMeasureKey): KpiFigure[] => {
    if (value === null) return []
    if (!key) return [{ label, value: fmt(value), tone: 'neutral' }]
    const c = comparisonText(changes[key], MEASURE[key])
    return [{ label, value: fmt(value), comparison: c.text ? c : undefined, tone: c.tone }]
  }
  const empty = (key: KpiCardKey, title: string, emptyText: string, lines: string[] = []): KpiCardModel => ({
    key,
    title,
    figures: [],
    lines,
    emptyText,
    charts: [],
  })

  const ttr = cur.time_to_resolve
  const timeToResolve: KpiCardModel =
    ttr.count === 0
      ? empty('time_to_resolve', 'Time to resolve', 'No tickets were resolved in this period.')
      : {
          key: 'time_to_resolve',
          title: 'Time to resolve',
          figures: [
            ...fig('Median', ttr.median_seconds, fmtDuration, 'time_to_resolve_median'),
            ...fig('90th percentile', ttr.p90_seconds, fmtDuration, 'time_to_resolve_p90'),
          ],
          lines: [`${plural(ttr.count, 'ticket')} resolved`],
          charts: [],
        }

  const fr = cur.first_response
  const firstResponse: KpiCardModel =
    fr.count === 0
      ? empty('first_response', 'First response', 'No ticket resolved in this period had an SLA first response.', slaLine)
      : {
          key: 'first_response',
          title: 'First response',
          figures: fig('Median', fr.median_seconds, fmtDuration, 'first_response_median'),
          lines: [`${plural(fr.count, 'ticket')} with an SLA first response`, ...slaLine],
          charts: [],
        }

  const sla = cur.sla
  const slaCard: KpiCardModel =
    sla.tracked === 0
      ? empty('sla', 'SLA attainment', 'No ticket resolved in this period was tracked by an SLA.', slaLine)
      : {
          key: 'sla',
          title: 'SLA attainment',
          figures: [
            ...fig('Met', sla.met_rate, pct, 'sla_met_rate'),
            ...fig('Response met', sla.response_met_rate, pct, 'sla_response_met_rate'),
            ...fig('Resolution met', sla.resolution_met_rate, pct, 'sla_resolution_met_rate'),
          ],
          lines: [`${plural(sla.tracked, 'ticket')} tracked`, ...slaLine],
          charts: [],
        }

  const reopen = cur.reopen
  const resolved = cur.resolved
  const reopenCard: KpiCardModel =
    resolved === 0
      ? empty('reopen', 'Reopen rate', 'No tickets were resolved in this period.')
      : {
          key: 'reopen',
          title: 'Reopen rate',
          figures: fig('Reopened', reopen.rate, pct, 'reopen_rate'),
          lines: [`${reopen.reopened} of ${resolved} resolved ${resolved === 1 ? 'ticket' : 'tickets'} had been reopened`],
          charts: [],
        }

  const vol = resp.volume
  const volume: KpiCardModel =
    cur.created === 0 && cur.resolved === 0
      ? empty('volume', 'Volume', 'No tickets were created or resolved in this period.')
      : {
          key: 'volume',
          title: 'Volume',
          figures: [
            ...fig('Created', cur.created, count, 'created'),
            ...fig('Resolved', cur.resolved, count, 'resolved'),
          ],
          lines: [],
          charts: [
            {
              key: 'priority',
              title: 'Created by priority',
              rows: vol.by_priority.map((p) => ({ key: p.priority, label: PRIORITY_LABEL[p.priority], value: p.count })),
            },
            {
              key: 'category',
              title: 'Created by category',
              rows: vol.by_category.map((c) => ({ key: c.id, label: c.name, value: c.count })),
            },
          ],
          days: vol.days,
        }

  const backlog: KpiCardModel =
    resp.backlog.total === 0
      ? empty('backlog', 'Backlog aging', 'No open tickets.')
      : {
          key: 'backlog',
          title: 'Backlog aging',
          figures: [{ label: 'Open now', value: count(resp.backlog.total), tone: 'neutral' }],
          lines: [],
          charts: [
            {
              key: 'age',
              title: 'Open tickets by age',
              rows: resp.backlog.buckets.map((b) => ({ key: b.key, label: BUCKET_LABEL[b.key] ?? b.key, value: b.count })),
            },
          ],
        }

  const wl = resp.workload
  const workload: KpiCardModel =
    wl.total === 0
      ? empty('workload', 'Workload', 'No open tickets.')
      : {
          key: 'workload',
          title: 'Workload',
          figures: [{ label: 'Open now', value: count(wl.total), tone: 'neutral' }],
          lines: [],
          charts: [
            {
              key: 'person',
              title: 'By person',
              rows: wl.by_assignee.map((r) => ({ key: r.id ?? 'nobody', label: r.name ?? 'Nobody', value: r.count })),
            },
            {
              key: 'group',
              title: 'By group',
              rows: wl.by_group.map((r) => ({ key: r.id ?? 'no-group', label: r.name ?? 'No group', value: r.count })),
            },
          ],
        }

  return [timeToResolve, firstResponse, slaCard, reopenCard, volume, backlog, workload]
}
