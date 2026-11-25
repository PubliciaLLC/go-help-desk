import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  THEME_COLOR,
  THEME_KEY,
  applyTheme,
  initialPreference,
  isPreference,
  readStored,
  resolve,
  systemPrefersDark,
  withLightTheme,
  writeStored,
} from './theme'
import type { ThemePreference } from './theme'

// The theme's pure rules (#709): what a stored value means, which of light and
// dark a preference resolves to, and how the choice reaches <html>. The same
// rules are in public/theme-init.js (theme-init.test.ts keeps them in step).

function storage(over: Partial<Storage> = {}): Storage {
  const m = new Map<string, string>()
  return {
    getItem: (k) => m.get(k) ?? null,
    setItem: (k, v) => void m.set(k, v),
    removeItem: (k) => void m.delete(k),
    clear: () => m.clear(),
    key: () => null,
    length: 0,
    ...over,
  } as Storage
}

const html = document.documentElement

function metas() {
  for (const name of ['color-scheme', 'theme-color']) {
    if (!document.querySelector(`meta[name="${name}"]`)) {
      const m = document.createElement('meta')
      m.name = name
      document.head.appendChild(m)
    }
  }
  return {
    scheme: document.querySelector('meta[name="color-scheme"]')!,
    color: document.querySelector('meta[name="theme-color"]')!,
  }
}

beforeEach(() => {
  html.classList.remove('dark')
  html.removeAttribute('data-theme-switching')
})
afterEach(() => vi.restoreAllMocks())

describe('resolve', () => {
  it.each<[ThemePreference, boolean, 'light' | 'dark']>([
    ['light', false, 'light'],
    ['light', true, 'light'],
    ['dark', false, 'dark'],
    ['dark', true, 'dark'],
    ['device', false, 'light'],
    ['device', true, 'dark'],
  ])('%s with the system dark = %s is %s', (pref, dark, want) => {
    expect(resolve(pref, dark)).toBe(want)
  })
})

describe('isPreference / initialPreference', () => {
  it('accepts exactly the three values', () => {
    for (const v of ['light', 'dark', 'device']) expect(isPreference(v)).toBe(true)
    for (const v of ['Dark', 'auto', 'system', '', null, undefined, 1, {}]) expect(isPreference(v)).toBe(false)
  })

  it('is the stored value, else device', () => {
    expect(initialPreference('dark')).toBe('dark')
    expect(initialPreference(null)).toBe('device')
  })
})

describe('readStored', () => {
  it.each(['light', 'dark', 'device'] as const)('reads %s', (v) => {
    const s = storage()
    s.setItem(THEME_KEY, v)
    expect(readStored(s)).toBe(v)
  })

  it.each(['Dark', 'auto', '', 'null'])('treats %j as nothing stored', (v) => {
    const s = storage()
    s.setItem(THEME_KEY, v)
    expect(readStored(s)).toBeNull()
  })

  it('reads nothing when there is nothing', () => {
    expect(readStored(storage())).toBeNull()
  })

  it('reads nothing when the storage refuses (a blocked site, some private modes)', () => {
    const s = storage({ getItem: () => { throw new DOMException('denied', 'SecurityError') } })
    expect(readStored(s)).toBeNull()
  })

  it('reads nothing without a storage', () => {
    expect(readStored(null)).toBeNull()
  })

  it('uses the key ghd.theme', () => {
    expect(THEME_KEY).toBe('ghd.theme')
    window.localStorage.setItem('ghd.theme', 'dark')
    expect(readStored()).toBe('dark')
    window.localStorage.removeItem('ghd.theme')
  })
})

describe('writeStored', () => {
  it('writes under the key', () => {
    const s = storage()
    writeStored('dark', s)
    expect(s.getItem(THEME_KEY)).toBe('dark')
  })

  it('ignores a storage that refuses (quota, blocked)', () => {
    const s = storage({ setItem: () => { throw new DOMException('full', 'QuotaExceededError') } })
    expect(() => writeStored('dark', s)).not.toThrow()
  })

  it('ignores a missing storage', () => {
    expect(() => writeStored('dark', null)).not.toThrow()
  })
})

describe('systemPrefersDark', () => {
  it('follows prefers-color-scheme', () => {
    const win = { matchMedia: (q: string) => ({ matches: q === '(prefers-color-scheme: dark)' }) } as unknown as Window
    expect(systemPrefersDark(win)).toBe(true)
  })

  it('is light when the browser has no matchMedia', () => {
    expect(systemPrefersDark({} as Window)).toBe(false)
  })
})

describe('applyTheme', () => {
  it('puts dark on <html> and takes it off again', () => {
    applyTheme('dark')
    expect(html.classList.contains('dark')).toBe(true)
    applyTheme('light')
    expect(html.classList.contains('dark')).toBe(false)
  })

  it('sets the colour-scheme and theme-color metas', () => {
    const { scheme, color } = metas()
    applyTheme('dark')
    expect(scheme.getAttribute('content')).toBe('dark')
    expect(color.getAttribute('content')).toBe(THEME_COLOR.dark)
    applyTheme('light')
    expect(scheme.getAttribute('content')).toBe('light')
    expect(color.getAttribute('content')).toBe(THEME_COLOR.light)
  })

  it('is idempotent, and a second call with the same theme starts no switch', () => {
    applyTheme('dark')
    html.removeAttribute('data-theme-switching')
    applyTheme('dark')
    expect(html.classList.contains('dark')).toBe(true)
    expect(html.hasAttribute('data-theme-switching')).toBe(false)
  })

  it('turns transitions off for the switch, and back on a frame later', () => {
    const frames: FrameRequestCallback[] = []
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation((cb) => frames.push(cb))
    applyTheme('dark')
    expect(html.hasAttribute('data-theme-switching')).toBe(true)
    frames.forEach((f) => f(0))
    expect(html.hasAttribute('data-theme-switching')).toBe(false)
  })

  it('uses the same colours as public/theme-init.js', () => {
    expect(THEME_COLOR).toEqual({ light: '#ffffff', dark: '#101828' })
  })
})

describe('withLightTheme', () => {
  it('runs with the dark class off and puts it back', () => {
    html.classList.add('dark')
    let during: boolean | null = null
    const got = withLightTheme(() => {
      during = html.classList.contains('dark')
      return 42
    })
    expect(got).toBe(42)
    expect(during).toBe(false)
    expect(html.classList.contains('dark')).toBe(true)
  })

  it('puts it back when the function throws', () => {
    html.classList.add('dark')
    expect(() => withLightTheme(() => { throw new Error('boom') })).toThrow('boom')
    expect(html.classList.contains('dark')).toBe(true)
  })

  it('leaves a light page light', () => {
    withLightTheme(() => undefined)
    expect(html.classList.contains('dark')).toBe(false)
  })

  it('turns transitions off while it runs, so a computed style is the light value and not the start of a fade, and leaves nothing behind', () => {
    html.classList.add('dark')
    let during: boolean | null = null
    withLightTheme(() => { during = html.hasAttribute('data-theme-switching') })
    expect(during).toBe(true)
    expect(html.hasAttribute('data-theme-switching')).toBe(false)
    // Also when fn throws.
    expect(() => withLightTheme(() => { throw new Error('x') })).toThrow('x')
    expect(html.hasAttribute('data-theme-switching')).toBe(false)
  })
})
