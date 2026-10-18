import { describe, expect, it } from 'vitest'
import { expiryToISO, maxExpiryDate, pastLimit, NO_LIMIT } from './expiry'

const now = new Date('2026-10-16T12:00:00')

describe('expiryToISO', () => {
  it('is undefined for an empty field, which means the key never expires', () => {
    expect(expiryToISO('')).toBeUndefined()
  })

  // The last second of the chosen day where the person is. Picking 4 March
  // must not make the key stop working on the evening of the 3rd.
  it('is the last second of that day in the local time zone', () => {
    const at = new Date(expiryToISO('2099-03-04')!)
    expect(at.getFullYear()).toBe(2099)
    expect(at.getMonth()).toBe(2)
    expect(at.getDate()).toBe(4)
    expect([at.getHours(), at.getMinutes(), at.getSeconds()]).toEqual([23, 59, 59])
  })

  it('is an RFC 3339 instant the server can parse', () => {
    expect(expiryToISO('2099-03-04')).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/)
  })

  // The last day the form allows ends later than "two years from now" does, so
  // it is sent as the limit itself, or the server would refuse the very date
  // the form offered.
  it('never goes past the limit, with an hour to spare', () => {
    const iso = expiryToISO(maxExpiryDate(2, now), 2, now)!
    const limit = new Date(now)
    limit.setFullYear(limit.getFullYear() + 2)
    expect(new Date(iso).getTime()).toBeLessThan(limit.getTime())
    expect(new Date(iso).getTime()).toBeGreaterThan(limit.getTime() - 2 * 3600 * 1000)
  })

  it('leaves an earlier day at the end of that day', () => {
    const at = new Date(expiryToISO('2027-01-05', 2, now)!)
    expect([at.getHours(), at.getMinutes()]).toEqual([23, 59])
  })
})

describe('maxExpiryDate and pastLimit', () => {
  it('is today plus the limit, as a date', () => {
    expect(maxExpiryDate(2, now)).toBe('2028-10-16')
    expect(maxExpiryDate(1, now)).toBe('2027-10-16')
  })

  it('is empty when there is no limit', () => {
    expect(maxExpiryDate(NO_LIMIT, now)).toBe('')
  })

  it('says a later day is past the limit, and the limit day is not', () => {
    expect(pastLimit('2028-10-16', 2, now)).toBe(false)
    expect(pastLimit('2028-10-17', 2, now)).toBe(true)
    expect(pastLimit('2099-01-01', NO_LIMIT, now)).toBe(false)
    expect(pastLimit('', 2, now)).toBe(false)
  })
})
