import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { TicketText } from './TicketText'

// #443 follow-up: a megabyte of plain text laid out as one block took seconds of
// main-thread time in a real browser (the post is never formatted, so this is the
// plain path). Text over the threshold is drawn as blocks the browser may skip
// while off screen; below it nothing changes.

describe('plain text rendering', () => {
  it('is one paragraph, as always, for ordinary text', () => {
    const { container } = render(<TicketText text={'line 1\nline 2'} format="plain" />)
    const p = container.querySelectorAll('p')
    expect(p).toHaveLength(1)
    expect(p[0].textContent).toBe('line 1\nline 2')
    expect(container.querySelector('[data-plain-chunk]')).toBeNull()
  })

  it('is a list of skippable blocks for a very long text, with every character present', () => {
    const text = 'a line of text\n'.repeat(20_000)
    const { container } = render(<TicketText text={text} format="plain" />)
    const chunks = Array.from(container.querySelectorAll<HTMLElement>('[data-plain-chunk]'))
    expect(chunks.length).toBeGreaterThan(10)
    // Nothing truncated: the blocks hold the whole text (their boundaries stand for newlines).
    expect(chunks.map((c) => c.textContent).join('\n')).toBe(text)
    for (const c of chunks) {
      expect(c.className).toContain('block')
      expect(c.className).toContain('content-visibility:auto')
      expect(c.style.containIntrinsicSize).toMatch(/^auto \d+lh$/)
    }
    expect(container.querySelector('p')).toBeNull()
  })

  it('a text over the formatting limit is the same, with its note', () => {
    const { container } = render(<TicketText text={'x '.repeat(40_000)} format="markdown" />)
    // (formatting is refused asynchronously; the first paint is the same plain path)
    expect(container.querySelector('[data-plain-chunk]')).not.toBeNull()
  })
})
