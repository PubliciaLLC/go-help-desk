import { describe, expect, it } from 'vitest'
import { act, fireEvent, render, screen } from '@testing-library/react'
import { renderNow } from '@/test/formatted'
import { TicketText } from './TicketText'

// Posts stored before 1.3.0 may be longer than 50,000 characters (a reply had no
// cap). They are shown collapsed: the start, a note that it is only the start,
// and a button for the whole text. Laying out a megabyte is slow, so it happens
// only when asked for, and then in one piece, so that selecting and copying the
// post gives back exactly what is stored.

const long = (n: number) => Array.from({ length: n }, (_, i) => `line ${i}`).join('\n')
const LONG = long(60_000) // about 500 KB

describe('a post over 50,000 characters', () => {
  it.each(['plain', 'markdown'] as const)('%s: starts collapsed, with the start of the text and a note', async (format) => {
    const { container } = await renderNow(<TicketText text={LONG} format={format} />)
    expect(container.textContent).toContain('line 0')
    expect(container.textContent).not.toContain('line 59999')
    expect(container.querySelector('[data-md-body]')).toBeNull()
    const note = screen.getByRole('note')
    expect(note.textContent).toMatch(/first 5,000 of [\d,]+ characters/i)
    expect(note.textContent).toMatch(/only part of the post/i)
    expect(screen.getByRole('button', { name: /show full text \(\d+(\.\d)? (KB|MB)\)/i }).getAttribute('aria-expanded')).toBe('false')
  })

  it('shows no plain/formatted toggle while collapsed (nothing over the limit is formatted)', async () => {
    await renderNow(<TicketText text={LONG} format="markdown" />)
    expect(screen.queryByRole('button', { name: /plain text|show formatted/i })).toBeNull()
  })

  it('the collapsed view holds only the start, so copying it cannot pass for the whole', async () => {
    const { container } = await renderNow(<TicketText text={LONG} format="plain" />)
    expect(container.textContent!.length).toBeLessThan(6_000 + 1_000)
  })

  it('expands to the whole text in one block, exactly as stored', async () => {
    const { container } = await renderNow(<TicketText text={LONG} format="plain" />)
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /show full text/i }))
    })
    const paragraphs = container.querySelectorAll('p.whitespace-pre-wrap')
    expect(paragraphs).toHaveLength(1)
    expect(paragraphs[0].textContent).toBe(LONG)
    expect(screen.getByRole('button', { name: /show only the start/i }).getAttribute('aria-expanded')).toBe('true')
    expect(screen.queryByRole('note')).toBeNull()
  })

  it('a Markdown post, expanded, is the whole text as written, with the usual reason', async () => {
    const { container } = await renderNow(<TicketText text={LONG} format="markdown" />)
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /show full text/i }))
    })
    expect(container.querySelector('[data-md-body]')).toBeNull()
    expect(container.textContent).toContain('line 59999')
    expect(container.textContent).toContain('too long to format')
  })

  it('collapses again', async () => {
    const { container } = await renderNow(<TicketText text={LONG} format="plain" />)
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /show full text/i }))
    })
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /show only the start/i }))
    })
    expect(container.textContent).not.toContain('line 59999')
    expect(screen.getByRole('note')).toBeTruthy()
  })

  it('does not cut a character in two at the end of the start', async () => {
    // A surrogate pair straddling the 5,000th unit.
    const text = 'a'.repeat(4_999) + '😀' + 'b'.repeat(60_000)
    const { container } = await renderNow(<TicketText text={text} format="plain" />)
    const shown = container.querySelector('p')!.textContent!
    expect(shown.endsWith('\uD83D')).toBe(false)
    expect(shown.length).toBeGreaterThanOrEqual(4_999)
  })

  it('a post at the limit is not collapsed', async () => {
    const { container } = await renderNow(<TicketText text={'a'.repeat(50_000)} format="plain" />)
    expect(container.querySelector('[role="note"]')).toBeNull()
    expect(container.querySelector('p')!.textContent).toHaveLength(50_000)
    render(<></>)
  })
})
