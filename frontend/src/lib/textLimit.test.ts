import { describe, expect, it } from 'vitest'
import { MAX_TEXT_BYTES, isOverLimit, nearLimit, textBytes } from './textLimit'

describe('text limit', () => {
  it('is the server limit, counted in bytes as the server counts', () => {
    expect(MAX_TEXT_BYTES).toBe(50_000)
    expect(textBytes('abc')).toBe(3)
    expect(textBytes('é')).toBe(2)
    expect(textBytes('日本')).toBe(6)
    expect(textBytes('😀')).toBe(4)
  })

  it('is over only past the limit', () => {
    expect(isOverLimit('a'.repeat(50_000))).toBe(false)
    expect(isOverLimit('a'.repeat(50_001))).toBe(true)
    expect(isOverLimit('é'.repeat(25_000))).toBe(false)
    expect(isOverLimit('é'.repeat(25_001))).toBe(true)
  })

  it('is near from 90 percent', () => {
    expect(nearLimit('a'.repeat(44_999))).toBe(false)
    expect(nearLimit('a'.repeat(45_000))).toBe(true)
  })
})
