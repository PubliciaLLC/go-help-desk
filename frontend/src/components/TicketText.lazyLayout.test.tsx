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
