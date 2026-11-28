import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen } from '@testing-library/react'

// #443: a page of many long posts must not format them all. Posts format when
// they are near the screen, and the page as a whole has a budget; the rest stay
// as written, with a note.

const formatMarkdown = vi.hoisted(() => vi.fn())
vi.mock('@/lib/markdownRunner', () => ({ formatMarkdown }))

import { TicketText } from './TicketText'
import { pageBudget } from '@/lib/pageBudget'

const tree = { type: 'root' as const, children: [{ type: 'text' as const, value: 'formatted' }] }

class FakeObserver {
  static all: FakeObserver[] = []
  cb: IntersectionObserverCallback
  els: Element[] = []
  constructor(cb: IntersectionObserverCallback) {
    this.cb = cb
    FakeObserver.all.push(this)
  }
  observe(el: Element) { this.els.push(el) }
  unobserve() {}
  disconnect() { this.els = [] }
  fire(isIntersecting: boolean) {
    this.cb(this.els.map((target) => ({ isIntersecting, target }) as IntersectionObserverEntry), this as unknown as IntersectionObserver)
  }
}

beforeEach(() => {
  formatMarkdown.mockReset()
  FakeObserver.all = []
  vi.stubGlobal('IntersectionObserver', FakeObserver)
})
afterEach(() => {
  vi.unstubAllGlobals()
  pageBudget.release(pageBudget.used)
})

describe('formatting only near the screen', () => {
  it('does not format a post that has not come near, and formats it when it does', async () => {
    formatMarkdown.mockResolvedValue({ ok: true, tree, nodes: 10 })
    const { container } = render(<TicketText text="**far**" format="markdown" />)
    await act(async () => {})
    expect(formatMarkdown).not.toHaveBeenCalled()
    expect(container.querySelector('[data-md-body]')).toBeNull()
    expect(container.textContent).toContain('**far**')

    await act(async () => FakeObserver.all[0].fire(true))
    expect(formatMarkdown).toHaveBeenCalledTimes(1)
    expect(container.querySelector('[data-md-body]')).not.toBeNull()
  })

  it('asks for a generous margin around the screen', async () => {
    const spy = vi.fn()
    class Spy extends FakeObserver {
      constructor(cb: IntersectionObserverCallback, opts?: IntersectionObserverInit) {
        super(cb)
        spy(opts)
      }
    }
    vi.stubGlobal('IntersectionObserver', Spy)
    render(<TicketText text="x" format="markdown" />)
    expect(parseInt(spy.mock.calls[0][0].rootMargin)).toBeGreaterThanOrEqual(1000)
  })

  it('a plain post is never observed or formatted', async () => {
    render(<TicketText text="plain" format="plain" />)
    await act(async () => {})
    expect(formatMarkdown).not.toHaveBeenCalled()
  })
})

describe('the page-wide budget', () => {
  it('shows posts over the budget as plain text with a note, and gives the share back when they go', async () => {
    formatMarkdown.mockResolvedValue({ ok: true, tree, nodes: 200_000 })
    const first = render(<TicketText text="one" format="markdown" />)
    await act(async () => FakeObserver.all[0].fire(true))
    expect(first.container.querySelector('[data-md-body]')).not.toBeNull()

    const second = render(<TicketText text="two" format="markdown" />)
    await act(async () => FakeObserver.all[1].fire(true))
    expect(second.container.querySelector('[data-md-body]')).toBeNull()
    expect(second.container.textContent).toContain('Shown as plain text')
    expect(second.container.textContent).toMatch(/formatted text|too much/i)

    first.unmount()
    const third = render(<TicketText text="three" format="markdown" />)
    await act(async () => FakeObserver.all[2].fire(true))
    expect(third.container.querySelector('[data-md-body]')).not.toBeNull()
  })
})

describe('why a post is plain', () => {
  it.each([
    ['unavailable', /formatting unavailable/i],
    ['long', /too long to format/i],
    ['complex', /too complex to format/i],
    ['timeout', /too complex to format/i],
  ])('%s', async (reason, note) => {
    formatMarkdown.mockResolvedValue({ ok: false, reason })
    vi.stubGlobal('IntersectionObserver', undefined)
    render(<TicketText text="x" format="markdown" />)
    await act(async () => {})
    expect(screen.getByText(note)).toBeTruthy()
    expect(screen.getByRole('button', { name: /plain text/i })).toBeTruthy()
  })
})
