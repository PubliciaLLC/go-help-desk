import { describe, expect, it } from 'vitest'
import { fmtAgo, fmtDuration, fmtMin, priorityVariant, slaTargetText } from './format'
import type { SLATargetStatus } from '@/api/types'

describe('priorityVariant', () => {
  it('maps each known priority to its badge variant', () => {
    expect(priorityVariant('critical')).toBe('destructive')
    expect(priorityVariant('high')).toBe('warning')
    expect(priorityVariant('medium')).toBe('default')
    expect(priorityVariant('low')).toBe('secondary')
  })

  // The mapping decides how urgent a ticket LOOKS. An unknown value must land
  // on the calm variant rather than the alarming one — a new priority added
  // server-side should not make every ticket render as critical.
  it('falls back to secondary for anything unrecognised', () => {
    expect(priorityVariant('')).toBe('secondary')
    expect(priorityVariant('URGENT')).toBe('secondary')
    expect(priorityVariant('Critical')).toBe('secondary') // case-sensitive by design
  })
})

describe('fmtMin', () => {
  it('renders under an hour in minutes', () => {
    expect(fmtMin(0)).toBe('0m')
    expect(fmtMin(1)).toBe('1m')
    expect(fmtMin(59)).toBe('59m')
  })

  // Whole hours omit the minutes. "2h 0m" is the obvious thing a rewrite would
  // produce and it reads badly in the SLA table.
  it('renders whole hours without a minutes part', () => {
    expect(fmtMin(60)).toBe('1h')
    expect(fmtMin(120)).toBe('2h')
    expect(fmtMin(1440)).toBe('24h')
  })

  it('renders hours and minutes together', () => {
    expect(fmtMin(90)).toBe('1h 30m')
    expect(fmtMin(61)).toBe('1h 1m')
    expect(fmtMin(2880 + 15)).toBe('48h 15m')
  })

  // The shipped SLA defaults, so the table these render into is covered by
  // name rather than by coincidence.
  it('renders the default SLA targets', () => {
    expect(fmtMin(480)).toBe('8h')
    expect(fmtMin(2880)).toBe('48h')
  })
})

function target(over: Partial<SLATargetStatus> = {}): SLATargetStatus {
  return {
    color: 'green',
    target_min: 60,
    elapsed_min: 12,
    remaining_min: 48,
    met_at: null,
    ...over,
  }
}

describe('slaTargetText', () => {
  it('reads "left" while outstanding and under target', () => {
    expect(slaTargetText(target({ remaining_min: 48 }))).toBe('48m left')
  })

  it('reads "over" while outstanding and past target', () => {
    expect(slaTargetText(target({ remaining_min: -80 }))).toBe('1h 20m over')
  })

  it('reads "to spare" for a target met before its deadline', () => {
    expect(slaTargetText(target({ met_at: '2026-09-25T10:12:00Z', remaining_min: 12 }))).toBe(
      'Met with 12m to spare',
    )
  })

  it('reads "late" for a target met after its deadline', () => {
    expect(slaTargetText(target({ met_at: '2026-09-25T10:12:00Z', remaining_min: -65 }))).toBe(
      'Met 1h 5m late',
    )
  })

  // The server truncates (target - elapsed) toward zero, so remaining_min is 0
  // for anything within a minute of the target on EITHER side. Only the color
  // tells them apart: red is "at or past 100%" (colorFor in domain/sla/status.go),
  // green and amber are strictly under it. A red zero must never read as on time.
  describe('remaining_min of 0', () => {
    const met = '2026-09-25T10:12:00Z'
    const cases: {
      name: string
      color: SLATargetStatus['color']
      met_at: string | null
      want: string
    }[] = [
      { name: 'outstanding, green, under a minute left', color: 'green', met_at: null, want: '0m left' },
      { name: 'outstanding, amber, under a minute left', color: 'amber', met_at: null, want: '0m left' },
      { name: 'outstanding, red, under a minute overdue', color: 'red', met_at: null, want: '<1m over' },
      { name: 'met, green, on time', color: 'green', met_at: met, want: 'Met with 0m to spare' },
      { name: 'met, amber, on time', color: 'amber', met_at: met, want: 'Met with 0m to spare' },
      { name: 'met, red, under a minute late', color: 'red', met_at: met, want: 'Met <1m late' },
    ]

    it.each(cases)('$name', ({ color, met_at, want }) => {
      expect(slaTargetText(target({ color, met_at, remaining_min: 0 }))).toBe(want)
    })
  })
})

describe('fmtAgo', () => {
  const now = new Date('2026-11-24T12:00:00Z')
  const S = 1000
  const M = 60 * S
  const H = 60 * M
  const D = 24 * H
  const before = (ms: number) => new Date(now.getTime() - ms).toISOString()

  const cases = [
    { name: '0 seconds', ms: 0, want: 'just now' },
    { name: '59 seconds', ms: 59 * S, want: 'just now' },
    { name: '60 seconds is one minute, singular', ms: 60 * S, want: '1 minute ago' },
    { name: '2 minutes, plural', ms: 2 * M, want: '2 minutes ago' },
    { name: '59 minutes 59 seconds floors to 59 minutes', ms: 59 * M + 59 * S, want: '59 minutes ago' },
    { name: '60 minutes is one hour, singular', ms: 60 * M, want: '1 hour ago' },
    { name: '61 minutes floors to one hour', ms: 61 * M, want: '1 hour ago' },
    { name: '23 hours 59 minutes floors to 23 hours', ms: 23 * H + 59 * M, want: '23 hours ago' },
    { name: '24 hours is one day, singular', ms: 24 * H, want: '1 day ago' },
    { name: '47 hours floors to one day', ms: 47 * H, want: '1 day ago' },
    { name: '2 days, plural', ms: 2 * D, want: '2 days ago' },
    { name: '120 days: no larger unit', ms: 120 * D, want: '120 days ago' },
    { name: 'the future (clock skew) is just now', ms: -5 * M, want: 'just now' },
  ]

  it.each(cases)('$name', ({ ms, want }) => {
    expect(fmtAgo(before(ms), now)).toBe(want)
  })

  it('defaults now to the current time', () => {
    expect(fmtAgo(new Date(Date.now() - 3 * H).toISOString())).toBe('3 hours ago')
  })
})

describe('fmtDuration', () => {
  // The Reports page reads a duration in whole seconds. Under a minute is
  // "under 1 min", never "0 min"; minutes drop out once the figure is days.
  const cases: Array<[string, number, string]> = [
    ['zero', 0, 'under 1 min'],
    ['59 seconds', 59, 'under 1 min'],
    ['60 seconds', 60, '1 min'],
    ['59 minutes', 59 * 60, '59 min'],
    ['1 hour', 3600, '1 h'],
    ['1 hour 1 minute', 3661, '1 h 1 min'],
    ['23 h 59 min', 23 * 3600 + 59 * 60, '23 h 59 min'],
    ['1 day', 86400, '1 d'],
    ['1 day 1 hour', 86400 + 3600, '1 d 1 h'],
    ['1 day 30 minutes drops the minutes', 86400 + 1800, '1 d'],
    ['400 days', 400 * 86400, '400 d'],
  ]

  it.each(cases)('$name', (_name, seconds, want) => {
    expect(fmtDuration(seconds)).toBe(want)
  })
})
