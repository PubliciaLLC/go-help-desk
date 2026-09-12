import { describe, expect, it } from 'vitest'
import { DEFAULT_SITE_NAME, siteName, siteTitle } from './branding'

describe('siteName', () => {
  it('uses the configured name', () => {
    expect(siteName('Acme IT Support')).toBe('Acme IT Support')
  })

  it('trims surrounding whitespace', () => {
    expect(siteName('  Acme IT  ')).toBe('Acme IT')
  })

  // The admin settings form writes "" when the field is cleared, so an empty
  // or blank value is the normal way an instance ends up unbranded. It must
  // behave exactly like an absent setting — an empty heading reads as a broken
  // page, which is how #56 presented.
  it('falls back when the setting is absent, empty or blank', () => {
    expect(siteName(undefined)).toBe(DEFAULT_SITE_NAME)
    expect(siteName(null)).toBe(DEFAULT_SITE_NAME)
    expect(siteName('')).toBe(DEFAULT_SITE_NAME)
    expect(siteName('   ')).toBe(DEFAULT_SITE_NAME)
    expect(siteName('\t\n')).toBe(DEFAULT_SITE_NAME)
  })

  it('never returns an empty string', () => {
    for (const input of [undefined, null, '', ' ', '\n', 'x']) {
      expect(siteName(input).length).toBeGreaterThan(0)
    }
  })
})

describe('siteTitle', () => {
  it('matches the site name so the tab and the page agree', () => {
    expect(siteTitle('Acme IT Support')).toBe('Acme IT Support')
    expect(siteTitle('')).toBe(DEFAULT_SITE_NAME)
  })

  // The bug in #57 was the literal string "frontend", Vite's scaffold default.
  it('is never the scaffold default', () => {
    expect(siteTitle(undefined)).not.toBe('frontend')
    expect(siteTitle('')).not.toBe('frontend')
  })
})
