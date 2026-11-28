import { describe, expect, it } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { BarChart } from './BarChart'

const rows = [
  { key: 'a', label: 'Hardware', value: 40 },
  { key: 'b', label: 'Network', value: 10 },
  { key: 'c', label: 'A category with a very long name that will not fit in the label column', value: 0 },
]

describe('BarChart', () => {
  it('draws one svg with a 28px row per bar, 18px high, no text in it', () => {
    const { container } = render(<BarChart rows={rows} ariaLabel="Tickets by category" />)
    const svgs = container.querySelectorAll('svg')
    expect(svgs).toHaveLength(1)
    const svg = svgs[0]
    expect(svg.getAttribute('role')).toBe('img')
    expect(svg.getAttribute('aria-label')).toBe('Tickets by category')
    expect(svg.getAttribute('viewBox')).toBe('0 0 100 84')
    expect(svg.getAttribute('preserveAspectRatio')).toBe('none')
    expect(svg.querySelector('text')).toBeNull()
    const rects = Array.from(svg.querySelectorAll('rect'))
    expect(rects).toHaveLength(3)
    rects.forEach((r, i) => {
      expect(r.getAttribute('height')).toBe('18')
      expect(r.getAttribute('fill')).toBe('currentColor')
      // Centred in its 28px row.
      expect(Number(r.getAttribute('y'))).toBe(i * 28 + 5)
    })
  })

  it('sizes each bar against the largest value', () => {
    const { container } = render(<BarChart rows={rows} ariaLabel="x" />)
    const w = Array.from(container.querySelectorAll('rect')).map((r) => Number(r.getAttribute('width')))
    expect(w).toEqual([100, 25, 0])
  })

  it('takes its colour from the tone, blue by default', () => {
    const { container, rerender } = render(<BarChart rows={rows} ariaLabel="x" />)
    expect(container.querySelector('svg')!.getAttribute('class')).toContain('text-chart-blue')
    rerender(<BarChart rows={rows} ariaLabel="x" tone="amber" />)
    expect(container.querySelector('svg')!.getAttribute('class')).toContain('text-chart-amber')
  })

  it('puts the full label in title on a one-line truncated label', () => {
    render(<BarChart rows={rows} ariaLabel="x" />)
    const long = rows[2].label
    const label = screen.getByTitle(long)
    expect(label.textContent).toBe(long)
    expect(label.className).toContain('truncate')
  })

  it('shows each value, through formatValue when given', () => {
    const { container } = render(<BarChart rows={rows} ariaLabel="x" formatValue={(n) => `${n} tickets`} />)
    expect(container.textContent).toContain('40 tickets')
    expect(container.textContent).toContain('10 tickets')
  })

  it('carries every row in a visually hidden table', () => {
    const { container } = render(<BarChart rows={rows} ariaLabel="Tickets by category" />)
    const table = container.querySelector('table')!
    expect(table.className).toContain('sr-only')
    const body = Array.from(table.querySelectorAll('tbody tr')) as HTMLElement[]
    expect(body).toHaveLength(3)
    expect(within(body[0]).getByText('Hardware')).toBeTruthy()
    expect(within(body[0]).getByText('40')).toBeTruthy()
  })

  it.each([
    ['no rows', []],
    ['every value zero', [{ key: 'a', label: 'A', value: 0 }]],
  ])('keeps its place and says so with %s', (_name, r) => {
    const { container } = render(<BarChart rows={r} ariaLabel="x" emptyText="Nothing in this period." />)
    expect(screen.getByText('Nothing in this period.')).toBeTruthy()
    expect(container.querySelector('svg')).toBeNull()
  })
})
