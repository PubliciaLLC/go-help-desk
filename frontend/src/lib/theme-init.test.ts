import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import { THEME_COLOR, THEME_KEY, initialPreference, readStored, resolve } from './theme'

// public/theme-init.js runs before the application has loaded, so it cannot
// import src/lib/theme.ts: it holds a second copy of the rule. This runs the
// file as the browser would, against a throwaway document, and requires the
// same answer as the real rule for every input, so the copies cannot drift.

const script = readFileSync('public/theme-init.js', 'utf8') // vitest runs from frontend/

type Stored = string | 'throws' | null

function run(stored: Stored, systemDark: boolean | 'no-matchmedia') {
  const doc = document.implementation.createHTMLDocument('t')
  doc.head.innerHTML = '<meta name="color-scheme" content="light dark"><meta name="theme-color" content="#ffffff">'
  const storage = {
    getItem: (k: string) => {
      if (stored === 'throws') throw new DOMException('denied', 'SecurityError')
      return k === THEME_KEY ? stored : null
    },
  }
  const win: Record<string, unknown> = { localStorage: storage }
  if (systemDark !== 'no-matchmedia') win.matchMedia = () => ({ matches: systemDark })
  new Function('window', 'document', script)(win, doc)
  return {
    dark: doc.documentElement.classList.contains('dark'),
    scheme: doc.querySelector('meta[name="color-scheme"]')!.getAttribute('content'),
    color: doc.querySelector('meta[name="theme-color"]')!.getAttribute('content'),
  }
}

/** What src/lib/theme.ts says for the same inputs. */
function expected(stored: Stored, systemDark: boolean) {
  const storage = {
    getItem: () => {
      if (stored === 'throws') throw new DOMException('denied', 'SecurityError')
      return stored
    },
  } as unknown as Storage
  return resolve(initialPreference(readStored(storage)), systemDark)
}

describe('public/theme-init.js', () => {
  const cases: [Stored, boolean][] = [
    ['light', false],
    ['light', true],
    ['dark', false],
    ['dark', true],
    ['device', false],
    ['device', true],
    [null, false],
    [null, true],
    ['throws', false],
    ['throws', true],
    ['auto', true],
    ['Dark', false],
    ['', true],
  ]

  it.each(cases)('stored %j, system dark = %s: the same answer as lib/theme.ts', (stored, systemDark) => {
    const want = expected(stored, systemDark)
    const got = run(stored, systemDark)
    expect(got.dark).toBe(want === 'dark')
    expect(got.scheme).toBe(want)
    expect(got.color).toBe(THEME_COLOR[want])
  })

  it('is light when the browser has no matchMedia and nothing is stored', () => {
    expect(run(null, 'no-matchmedia').dark).toBe(false)
  })

  it('still honours a stored dark with no matchMedia', () => {
    expect(run('dark', 'no-matchmedia').dark).toBe(true)
  })

  it('needs no imports and stays small', () => {
    expect(script).not.toMatch(/\bimport\b|\brequire\(/)
    expect(script.split('\n').length).toBeLessThan(30)
  })
})
