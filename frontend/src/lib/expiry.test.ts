import { describe, expect, it } from 'vitest'
import { expiryToISO } from './expiry'

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
})
