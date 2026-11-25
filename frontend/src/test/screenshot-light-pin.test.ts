import { readFileSync } from 'node:fs'
import { describe, expect, it, vi } from 'vitest'

// #709: the website screenshots are light, whatever an account or a browser says.
// scripts/screenshots/capture.mjs pins them (a light colour scheme, ghd.theme=light
// stored before every page load) and refuses to save a shot of a dark page. The
// Docker pipeline is not run here; this keeps the pin from being lost.

// The script is plain JavaScript outside src, so it has no types.
// @ts-expect-error TS7016: no declaration file for an .mjs script
const load = () => import('../../scripts/screenshots/lightPin.mjs') as Promise<{
  pinLight: (ctx: { addInitScript: (fn: () => void) => Promise<void> }) => Promise<void>
  assertLight: (page: { evaluate: (fn: () => boolean) => Promise<boolean> }, name: string) => Promise<void>
}>

describe('the screenshot pipeline stays light', () => {
  it('stores ghd.theme=light before each page load', async () => {
    const { pinLight } = await load()
    let script: () => void = () => {}
    await pinLight({ addInitScript: async (fn) => { script = fn } })
    window.localStorage.setItem('ghd.theme', 'dark')
    script()
    expect(window.localStorage.getItem('ghd.theme')).toBe('light')
  })

  it('refuses a dark page, naming the shot', async () => {
    const { assertLight } = await load()
    document.documentElement.classList.add('dark')
    const page = { evaluate: vi.fn(async (fn: () => boolean) => fn()) }
    await expect(assertLight(page, '05-ticket.png')).rejects.toThrow(/05-ticket\.png is dark: screenshots must be light/)
    document.documentElement.classList.remove('dark')
    await expect(assertLight(page, '05-ticket.png')).resolves.toBeUndefined()
  })

  it('is wired into capture.mjs: a light colour scheme, the pin, and the check before each shot', () => {
    const src = readFileSync('scripts/screenshots/capture.mjs', 'utf8')
    expect(src).toMatch(/colorScheme: 'light'/)
    expect(src).toMatch(/await pinLight\(ctx\)/)
    expect(src).toMatch(/await assertLight\(page, shot\.file\)/)
    // The check comes after the page is prepared and before the picture is taken.
    expect(src.indexOf('await assertLight(page, shot.file)')).toBeGreaterThan(src.indexOf('await shot.prepare(page, ctx, s)'))
    expect(src.indexOf('await assertLight(page, shot.file)')).toBeLessThan(src.indexOf('const buf = await page.screenshot(screenshotOpts)'))
  })
})
