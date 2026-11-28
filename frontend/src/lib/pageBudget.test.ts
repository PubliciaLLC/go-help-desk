import { describe, expect, it } from 'vitest'
import { PageBudget } from './pageBudget'

describe('PageBudget', () => {
  it('admits work up to its limit and refuses the rest', () => {
    const b = new PageBudget(100)
    expect(b.reserve(60)).toBe(true)
    expect(b.reserve(40)).toBe(true)
    expect(b.reserve(1)).toBe(false)
  })

  it('takes work back when it is released, so a page that is left frees its share', () => {
    const b = new PageBudget(100)
    b.reserve(100)
    expect(b.reserve(1)).toBe(false)
    b.release(100)
    expect(b.reserve(100)).toBe(true)
  })

  it('never goes negative on a double release', () => {
    const b = new PageBudget(10)
    b.reserve(5)
    b.release(5)
    b.release(5)
    expect(b.reserve(10)).toBe(true)
    expect(b.reserve(1)).toBe(false)
  })
})
