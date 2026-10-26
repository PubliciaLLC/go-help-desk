import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { TextLimit } from './TextLimit'

const bytes = (n: number) => 'a'.repeat(n)

describe('TextLimit: what is shown', () => {
  it('says nothing for short text', () => {
    const { container } = render(<TextLimit value="hello" />)
    expect(container.textContent).toBe('')
  })

  it('shows the count in bytes near the limit', () => {
    const { container } = render(<TextLimit value={bytes(46_000)} />)
    expect(container.textContent).toContain('46,000 / 50,000 bytes')
    expect(container.querySelector('[role="alert"]')).toBeNull()
  })

  // Integer arithmetic: 3,500 / 50,000 * 100 is 7.000000000000001 in floating
  // point, which rounded up to 8.
  it.each([
    [50_001, 1],
    [50_500, 1],
    [50_501, 2],
    [53_500, 7],
    [55_000, 10],
    [57_000, 14],
    [60_000, 20],
    [100_000, 100],
  ])('%i bytes is %i%% over', (n, pct) => {
    const { container } = render(<TextLimit value={bytes(n)} />)
    expect(container.textContent).toContain(`This is ${pct}% over the size limit (50,000 bytes).`)
  })

  it('explains bytes and never says characters', () => {
    const { container } = render(<TextLimit value={bytes(50_250)} />)
    expect(container.textContent).toContain('Letters outside basic English and emoji take 2 to 4 bytes each.')
    expect(container.textContent).not.toMatch(/character/i)
  })
})

// A screen reader hears a change once, not on every keystroke. The visible count
// sits in no live region; crossing 90 percent is announced once; going over is
// announced once, by the alert alone.
describe('TextLimit: what a screen reader hears', () => {
  const live = (c: HTMLElement) => c.querySelector('[aria-live="polite"]') as HTMLElement
  const count = (c: HTMLElement) => c.textContent!.match(/[\d,]+ \/ 50,000 bytes/)

  it('keeps the visible count out of every live region', () => {
    const { container } = render(<TextLimit value={bytes(46_000)} />)
    const counter = Array.from(container.querySelectorAll('p')).find((p) => /50,000 bytes/.test(p.textContent!))!
    expect(counter.closest('[aria-live], [role="alert"]')).toBeNull()
  })

  it('has a polite status region that is empty until something is announced', () => {
    const { container } = render(<TextLimit value="" />)
    expect(live(container).getAttribute('aria-live')).toBe('polite')
    expect(live(container).textContent).toBe('')
  })

  it('announces "Approaching the size limit" once on crossing 90 percent, and not again as you type', () => {
    const { container, rerender } = render(<TextLimit value={bytes(40_000)} />)
    const seen: string[] = []
    new MutationObserver(() => seen.push(live(container).textContent!)).observe(live(container), {
      childList: true, characterData: true, subtree: true,
    })
    rerender(<TextLimit value={bytes(45_000)} />)
    expect(live(container).textContent).toBe('Approaching the size limit')
    for (const n of [45_001, 45_500, 47_000, 49_999, 50_000]) rerender(<TextLimit value={bytes(n)} />)
    expect(live(container).textContent).toBe('Approaching the size limit')
    return Promise.resolve().then(() => {
      // One change in the live region in total, despite six keystrokes' worth of updates.
      expect(seen.length).toBeLessThanOrEqual(1)
      expect(count(container)).not.toBeNull()
    })
  })

  it('going over is announced by the alert alone: one alert, a quiet status region, and a constant message', () => {
    const { container, rerender, getAllByRole } = render(<TextLimit value={bytes(49_000)} />)
    rerender(<TextLimit value={bytes(50_001)} />)
    expect(getAllByRole('alert')).toHaveLength(1)
    expect(live(container).textContent).toBe('')
    const first = getAllByRole('alert')[0].textContent
    rerender(<TextLimit value={bytes(53_500)} />)
    expect(getAllByRole('alert')).toHaveLength(1)
    // The message the alert carries does not change as the count does, so it is not read again.
    expect(getAllByRole('alert')[0].textContent).toBe(first)
    expect(first).toMatch(/over the size limit \(50,000 bytes\)/)
  })

  it('the visible percent is not inside the alert', () => {
    const { container, getByRole } = render(<TextLimit value={bytes(53_500)} />)
    expect(getByRole('alert').textContent).not.toMatch(/\d+%/)
    expect(container.textContent).toContain('7% over the size limit')
  })

  it('announces coming back under the limit once', () => {
    const { container, rerender } = render(<TextLimit value={bytes(50_100)} />)
    rerender(<TextLimit value={bytes(46_000)} />)
    expect(live(container).textContent).toBe('Back under the size limit')
    expect(container.querySelector('[role="alert"]')).toBeNull()
  })

  it('clears the announcement when the text is short again', () => {
    const { container, rerender } = render(<TextLimit value={bytes(46_000)} />)
    rerender(<TextLimit value={bytes(100)} />)
    expect(live(container).textContent).toBe('')
  })
})
