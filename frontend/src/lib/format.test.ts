import { describe, expect, it } from 'vitest'
import { fmtMin, priorityVariant } from './format'

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
