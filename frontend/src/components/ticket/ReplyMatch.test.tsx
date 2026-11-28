import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ReplyMatchLine } from './ReplyMatch'
import { splitSnippet } from '@/lib/replySnippet'
import type { ReplyMatch } from '@/api/types'

const START = '\u0002'
const STOP = '\u0003'

describe('splitSnippet', () => {
  it('splits on the server markers: matched words are marked, the rest is not', () => {
    expect(splitSnippet(`…creasing at the ${START}fuser${STOP}, so I have ordered a new ${START}fuser${STOP} kit…`)).toEqual([
      { text: '…creasing at the ', match: false },
      { text: 'fuser', match: true },
      { text: ', so I have ordered a new ', match: false },
      { text: 'fuser', match: true },
      { text: ' kit…', match: false },
    ])
  })

  it('text with no markers is one plain piece', () => {
    expect(splitSnippet('nothing marked')).toEqual([{ text: 'nothing marked', match: false }])
  })

  // A reply may contain U+0002 and U+0003 itself. The worst it may do is move
  // a highlight (docs/plans/advanced-search.md, section 2.5).
  it('stray and nested markers never throw and never leak into the text', () => {
    for (const s of [`a${STOP}b`, `a${START}b`, `${START}${START}x${STOP}${STOP}`, `${STOP}${START}`, START, STOP, '']) {
      const out = splitSnippet(s)
      const joined = out.map(p => p.text).join('')
      expect(joined.includes(START) || joined.includes(STOP)).toBe(false)
    }
  })

  it('an unclosed marker runs to the end', () => {
    expect(splitSnippet(`a ${START}b c`)).toEqual([
      { text: 'a ', match: false },
      { text: 'b c', match: true },
    ])
  })
})

function match(over: Partial<ReplyMatch> = {}): ReplyMatch {
  return {
    reply_id: 'r1',
    internal: false,
    created_at: '2026-11-03T14:02:00Z',
    snippet: `…paper is creasing at the ${START}fuser${STOP}, so I have ordered…`,
    ...over,
  }
}

describe('ReplyMatchLine', () => {
  it('says it was a public reply, and when', () => {
    render(<ReplyMatchLine match={match()} />)
    expect(screen.getByText(/^Public reply · /)).toBeTruthy()
    expect(screen.queryByText(/Internal note/)).toBeNull()
  })

  it('says it was an internal note, and when', () => {
    render(<ReplyMatchLine match={match({ internal: true })} />)
    expect(screen.getByText(/^Internal note · /)).toBeTruthy()
  })

  it('wraps each matched word in <mark>, and nothing else', () => {
    const { container } = render(<ReplyMatchLine match={match()} />)
    const marks = [...container.querySelectorAll('mark')]
    expect(marks.map(m => m.textContent)).toEqual(['fuser'])
    expect(container.textContent).toContain('paper is creasing at the fuser, so I have ordered')
  })

  it('shows no author name: the match carries none, and the line adds none', () => {
    const { container } = render(<ReplyMatchLine match={match()} />)
    expect(container.textContent).not.toMatch(/\bby\b/i)
  })

  // The snippet is a reply written by anyone, including a guest. It is text.
  it('renders HTML in a reply as the characters it is, never as elements', () => {
    const evil = `<img src=x onerror="alert(1)"> and ${START}<script>alert(2)</script>${STOP} <a href="javascript:alert(3)">x</a>`
    const { container } = render(<ReplyMatchLine match={match({ snippet: evil })} />)

    expect(container.querySelector('img, script, a, iframe, object, embed')).toBeNull()
    expect(container.querySelectorAll('svg')).toHaveLength(1) // the line's own icon
    expect(container.textContent).toContain('<img src=x onerror="alert(1)">')
    expect(container.querySelector('mark')?.textContent).toBe('<script>alert(2)</script>')
  })

  it('an unreadable date does not break the line', () => {
    render(<ReplyMatchLine match={match({ created_at: 'not a date' })} />)
    expect(screen.getByText(/^Public reply/)).toBeTruthy()
  })
})
