import type { ThemePreference } from '@/api/types'

// The theme's pure rules (#709): no React, no store. public/theme-init.js holds
// the same rules for the moment before this code has loaded, and
// theme-init.test.ts keeps the two in step.

export type { ThemePreference }
export type ResolvedTheme = 'light' | 'dark'

/** The one thing kept in localStorage: the last choice made on this browser. */
export const THEME_KEY = 'ghd.theme'

// The colours of the browser's own chrome (a phone's address bar): white, and the dark top bar's.
export const THEME_COLOR: Record<ResolvedTheme, string> = { light: '#ffffff', dark: '#101828' } // theme-ok: browser chrome colour, must equal public/theme-init.js

export function isPreference(v: unknown): v is ThemePreference {
  return v === 'light' || v === 'dark' || v === 'device'
}

function defaultStorage(): Storage | null {
  // Even reading the property can throw (a blocked site).
  try {
    return window.localStorage
  } catch {
    return null
  }
}

/** The stored choice, or null when there is none, it is not one of the three values, or storage refuses. */
export function readStored(storage: Storage | null = defaultStorage()): ThemePreference | null {
  try {
    const v = storage?.getItem(THEME_KEY)
    return isPreference(v) ? v : null
  } catch {
    return null
  }
}

/** Remembers the choice. A browser that refuses storage just forgets it. */
export function writeStored(p: ThemePreference, storage: Storage | null = defaultStorage()): void {
  try {
    storage?.setItem(THEME_KEY, p)
  } catch {
    // Quota, a blocked site, a private mode: the choice still applies to this page.
  }
}

/** Whether the browser asks for dark. A browser without matchMedia (jsdom) is light. */
export function systemPrefersDark(win: Window = window): boolean {
  return typeof win.matchMedia === 'function' && win.matchMedia('(prefers-color-scheme: dark)').matches
}

export function resolve(p: ThemePreference, prefersDark: boolean): ResolvedTheme {
  return p === 'dark' || (p === 'device' && prefersDark) ? 'dark' : 'light'
}

/** What a page shows before it knows the account's choice. */
export function initialPreference(stored: ThemePreference | null): ThemePreference {
  return stored ?? 'device'
}

/**
 * Puts the resolved theme on <html> (the `dark` class), on the colour-scheme
 * meta (native controls) and on the theme-color meta (browser chrome). When the
 * theme actually changes, transitions are off for one frame so buttons with
 * transition-colors do not fade between themes.
 */
export function applyTheme(t: ResolvedTheme, doc: Document = document): void {
  const html = doc.documentElement
  const dark = t === 'dark'
  if (html.classList.contains('dark') !== dark) {
    html.setAttribute('data-theme-switching', '')
    html.classList.toggle('dark', dark)
    // Style has to be computed with transitions off before the attribute goes,
    // or the browser would compute it after and transition anyway.
    void html.offsetHeight
    const win = doc.defaultView ?? window
    const frame = win.requestAnimationFrame?.bind(win) ?? ((cb: () => void) => win.setTimeout(cb, 16))
    frame(() => html.removeAttribute('data-theme-switching'))
  }
  doc.querySelector('meta[name="color-scheme"]')?.setAttribute('content', t)
  // The first theme-color meta is the one browsers use and the one driven here.
  // A second one, added by the server for an uploaded icon (#708), holds the
  // light colour; same rule as public/theme-init.js.
  const tcs = doc.querySelectorAll('meta[name="theme-color"]')
  if (tcs.length) {
    const light = tcs.length > 1 ? (tcs[tcs.length - 1].getAttribute('content') ?? THEME_COLOR.light) : THEME_COLOR.light
    tcs[0].setAttribute('content', dark ? THEME_COLOR.dark : light)
  }
}

/**
 * Runs `fn` with the page in the light theme and restores it: the PNG export
 * reads the page this way so an image is the same whichever theme the person
 * uses. `fn` is synchronous, so no frame is painted in between.
 */
export function withLightTheme<T>(fn: () => T, doc: Document = document): T {
  const html = doc.documentElement
  const wasDark = html.classList.contains('dark')
  // Transitions off while the class is removed: an element with
  // transition-colors would otherwise answer getComputedStyle with the dark
  // value it is fading from. Same mechanism as applyTheme.
  const hadAttr = html.hasAttribute('data-theme-switching')
  html.setAttribute('data-theme-switching', '')
  if (wasDark) html.classList.remove('dark')
  try {
    return fn()
  } finally {
    if (wasDark) html.classList.add('dark')
    // Computed with transitions still off, so the way back is not a fade either.
    void html.offsetHeight
    if (!hadAttr) html.removeAttribute('data-theme-switching')
  }
}
