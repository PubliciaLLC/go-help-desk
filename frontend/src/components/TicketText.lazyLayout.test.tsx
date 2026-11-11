/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import { renderNow } from '@/test/formatted'
import { TicketText } from './TicketText'

// A page of long plain-text posts (220 replies of 50 KB measured 3.3 s of
// layout) lets the browser skip laying out the ones off screen, with
// content-visibility: auto. jsdom has no layout, so these pin what the markup
// asks for; frontend/e2e/markdown-dos.spec.ts and long-text.spec.ts measure
// what the browser does with it.

const lines = (n: number) => Array.from({ length: n }, (_, i) => `line ${i}`).join('\n')
const plainParagraph = (c: HTMLElement) => c.querySelector('p.whitespace-pre-wrap') as HTMLElement

describe('lazy layout of long plain-text posts', () => {
  it('a long plain post is marked for lazy layout', async () => {
    const { container } = await renderNow(<TicketText text={lines(3000)} format="plain" />)
    expect(plainParagraph(container).classList.contains('lazy-layout')).toBe(true)
  })

  it('a short plain post is laid out as usual', async () => {
    const { container } = await renderNow(<TicketText text={lines(20)} format="plain" />)
    const p = plainParagraph(container)
    expect(p.classList.contains('lazy-layout')).toBe(false)
  })

  it('the collapsed start of a very old post is short and is not lazy', async () => {
    const { container } = await renderNow(<TicketText text={lines(60_000)} format="plain" />)
    expect(plainParagraph(container).classList.contains('lazy-layout')).toBe(false)
  })

  it('does not change the text', async () => {
    const text = lines(3000)
    const { container } = await renderNow(<TicketText text={text} format="plain" />)
    expect(plainParagraph(container).textContent).toBe(text)
  })
})

// A browser that has content-visibility but not the lh unit (Chrome 95 to 108)
// would drop an estimate written in lh, and a skipped post would have no height.
// That cannot be a fallback declaration written before it: a declaration that
// uses var() is valid at parse time whatever its unit, so the later one would win
// and then be invalid. @supports is the gate, and the unit it does not need is
// the fallback outside it.
describe('the lazy layout stylesheet', () => {
  // Read from disk: vitest replaces imported CSS, ?raw included, with an empty string.
  const css = readFileSync('src/index.css', 'utf8')
  const rule = css.slice(css.indexOf('.lazy-layout {'))
  const supports = rule.slice(rule.indexOf('@supports (height: 1lh)'))
  const outside = rule.slice(0, rule.indexOf('@supports (height: 1lh)'))

  it('skips posts that are off screen', () => {
    expect(outside).toMatch(/content-visibility:\s*auto/)
  })
  it('has a height estimate that needs no lh unit', () => {
    expect(outside).toMatch(/contain-intrinsic-block-size:\s*auto calc\(var\(--text-rows, 1\) \* 1\.5em\)/)
    expect(outside).not.toMatch(/\dlh/)
  })
  it('uses the lh unit only where the browser understands it', () => {
    expect(rule).toContain('@supports (height: 1lh)')
    expect(supports).toMatch(/contain-intrinsic-block-size:\s*auto calc\(var\(--text-rows, 1\) \* 1lh\)/)
  })
})

