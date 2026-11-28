import { describe, expect, it } from 'vitest'
import { charCount, hasOwnLimit, maxLengthHint, overLimit, parseMaxLength, typeCap } from './fieldLimits'

describe('typeCap and hasOwnLimit', () => {
  it('is 255 for single-line types and 10,000 for a text area', () => {
    expect(typeCap('text')).toBe(255)
    expect(typeCap('number')).toBe(255)
    expect(typeCap('select')).toBe(255)
    expect(typeCap('textarea')).toBe(10_000)
  })
  it('lets only text and text area have a limit of their own', () => {
    expect(hasOwnLimit('text')).toBe(true)
    expect(hasOwnLimit('textarea')).toBe(true)
    expect(hasOwnLimit('number')).toBe(false)
    expect(hasOwnLimit('select')).toBe(false)
  })
})

describe('maxLengthHint', () => {
  it('says what the type allows', () => {
    expect(maxLengthHint('text')).toBe('Up to 254 characters. Leave empty for 255.')
    expect(maxLengthHint('textarea')).toBe('Up to 9,999 characters. Leave empty for 10,000.')
  })
})

describe('parseMaxLength', () => {
  it('reads empty as none', () => {
    expect(parseMaxLength('', 'text')).toEqual({ ok: true, value: null })
    expect(parseMaxLength('   ', 'text')).toEqual({ ok: true, value: null })
  })
  it.each([['1', 1], ['100', 100], ['254', 254], [' 50 ', 50]])('accepts %s for text', (text, n) => {
    expect(parseMaxLength(text, 'text')).toEqual({ ok: true, value: n })
  })
  it('accepts up to 9,999 for a text area', () => {
    expect(parseMaxLength('9999', 'textarea')).toEqual({ ok: true, value: 9999 })
    expect(parseMaxLength('255', 'textarea')).toEqual({ ok: true, value: 255 })
  })
  it.each(['0', '255', '256', '10000', '-1', '1.5', '1e2', 'abc', '0x10'])('refuses %s for text', (text) => {
    const r = parseMaxLength(text, 'text')
    expect(r.ok).toBe(false)
    expect(r.ok === false && r.message).toBe('The maximum length must be a whole number between 1 and 254.')
  })
  it('refuses the text area cap and above for a text area, naming 9,999', () => {
    const r = parseMaxLength('10000', 'textarea')
    expect(r.ok === false && r.message).toBe('The maximum length must be a whole number between 1 and 9,999.')
  })
})

describe('charCount and overLimit', () => {
  it('counts code points, not UTF-16 units', () => {
    expect(charCount('a😀中')).toBe(3)
    expect('😀'.length).toBe(2)
  })
  it('says nothing for a value that fits, or a field with no limit', () => {
    expect(overLimit('F', 'abc', 3)).toBe('')
    expect(overLimit('F', 'abc', undefined)).toBe('')
    expect(overLimit('F', '😀'.repeat(10), 10)).toBe('')
  })
  it('names the field and the limit, with a thousands separator', () => {
    expect(overLimit('Log', 'a'.repeat(10001), 10000)).toBe('Log is too long: at most 10,000 characters.')
    expect(overLimit('F', '😀'.repeat(11), 10)).toBe('F is too long: at most 10 characters.')
  })
})
