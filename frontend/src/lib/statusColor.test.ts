import { describe, expect, it } from 'vitest'
import { THEME_COLOR } from '@/lib/theme'
import { contrastRatio, readableStatusColor } from './statusColor'

// The dark card the status text sits on.
const DARK_CARD = THEME_COLOR.dark

describe('readableStatusColor', () => {
  it('returns the administrator-chosen colour unchanged in light', () => {
    for (const c of ['#1e3a8a', '#ffffff', '#6b7280', '#FFAA00']) {
      expect(readableStatusColor(c, 'light')).toBe(c)
    }
  })

  it('makes a dark navy readable on the dark card', () => {
    const navy = '#1e3a8a'
    expect(contrastRatio(navy, DARK_CARD)).toBeLessThan(4.5)
    const out = readableStatusColor(navy, 'dark')
    expect(out).not.toBe(navy)
    expect(contrastRatio(out, DARK_CARD)).toBeGreaterThanOrEqual(4.5)
  })

  it('leaves a colour that is already readable alone in dark', () => {
    expect(readableStatusColor('#ffffff', 'dark')).toBe('#ffffff')
    expect(readableStatusColor('#93c5fd', 'dark')).toBe('#93c5fd')
  })

  it('stays hex and never loses the hue entirely before the last step', () => {
    const out = readableStatusColor('#7f1d1d', 'dark')
    expect(out).toMatch(/^#[0-9a-f]{6}$/)
    expect(contrastRatio(out, DARK_CARD)).toBeGreaterThanOrEqual(4.5)
  })

  it('expands three-digit hex', () => {
    const out = readableStatusColor('#003', 'dark')
    expect(contrastRatio(out, DARK_CARD)).toBeGreaterThanOrEqual(4.5)
  })

  it('returns anything that is not a hex colour as it is', () => {
    expect(readableStatusColor('', 'dark')).toBe('')
    expect(readableStatusColor('red', 'dark')).toBe('red')
    expect(readableStatusColor('var(--x)', 'dark')).toBe('var(--x)')
    expect(readableStatusColor(undefined, 'dark')).toBeUndefined()
  })
})
