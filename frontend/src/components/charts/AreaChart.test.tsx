import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { AreaChart } from './AreaChart'
import type { AreaChartProps } from './AreaChart'

const labels = ['2026-10-01', '2026-10-02', '2026-10-03']

function props(over: Partial<AreaChartProps> = {}): AreaChartProps {
  return {
    labels,
    series: [
      { key: 'created', label: 'Created', values: [1, 4, 7], tone: 'blue' },
      { key: 'resolved', label: 'Resolved', values: [0, 2, 3], tone: 'green' },
    ],
    ariaLabel: 'Tickets created and resolved per day',
    ...over,
  }
}

describe('AreaChart: marks', () => {
  it('renders one area and one line per series, and one baseline', () => {
    const { container } = render(<AreaChart {...props()} />)
    const svg = container.querySelector('svg')!
    expect(svg.getAttribute('role')).toBe('img')
    expect(svg.getAttribute('aria-label')).toBe('Tickets created and resolved per day')
    expect(svg.getAttribute('viewBox')).toBe('0 0 100 160')
    expect(svg.getAttribute('preserveAspectRatio')).toBe('none')
    expect(svg.querySelectorAll('path[fill-opacity="0.15"]')).toHaveLength(2)
    expect(svg.querySelectorAll('path[vector-effect="non-scaling-stroke"]')).toHaveLength(2)
    expect(svg.querySelectorAll('line')).toHaveLength(1)
  })

  it('has no text inside the svg', () => {
    const { container } = render(<AreaChart {...props()} />)
    expect(container.querySelector('svg')!.textContent).toBe('')
  })

  it('uses the given height in the viewBox', () => {
    const { container } = render(<AreaChart {...props({ height: 90 })} />)
    expect(container.querySelector('svg')!.getAttribute('viewBox')).toBe('0 0 100 90')
  })
})

describe('AreaChart: text outside the picture', () => {
  it('shows the axis maximum as the nice ceiling of the largest value', () => {
    const { container } = render(<AreaChart {...props()} />)
    expect(container.textContent).toContain('10')
  })

  it('formats the axis maximum with formatValue', () => {
    const { container } = render(<AreaChart {...props({ formatValue: (v) => `${v} tickets` })} />)
    expect(container.textContent).toContain('10 tickets')
  })

  it('labels the first and last x values with formatLabel', () => {
    const { container } = render(
      <AreaChart {...props({ formatLabel: (l) => `d:${l.slice(-2)}` })} />,
    )
    expect(container.textContent).toContain('d:01')
    expect(container.textContent).toContain('d:03')
  })

  it('puts every value in a visually hidden table', () => {
    const { container } = render(<AreaChart {...props({ formatLabel: (l) => `L${l.slice(-2)}` })} />)
    const table = container.querySelector('table.sr-only')!
    expect(table).not.toBeNull()
    const rows = Array.from(table.querySelectorAll('tbody tr')).map((tr) =>
      Array.from(tr.querySelectorAll('th, td')).map((c) => c.textContent),
    )
    expect(rows).toEqual([
      ['L01', '1', '0'],
      ['L02', '4', '2'],
      ['L03', '7', '3'],
    ])
  })

  it('shows each series label in the legend', () => {
    const { container } = render(<AreaChart {...props()} />)
    const legend = container.querySelector('figcaption')!
    expect(legend.textContent).toContain('Created')
    expect(legend.textContent).toContain('Resolved')
  })
})

function legendItems(container: HTMLElement): string[][] {
  return Array.from(container.querySelectorAll('figcaption li')).map((li) =>
    Array.from(li.querySelectorAll('span:not([aria-hidden])')).map((s) => s.textContent ?? ''),
  )
}

describe('AreaChart: legend totals', () => {
  it('shows each series total next to its label', () => {
    const { container } = render(<AreaChart {...props()} />)
    expect(legendItems(container)).toEqual([
      ['Created', '12'],
      ['Resolved', '5'],
    ])
  })

  it('formats the totals with formatValue', () => {
    const { container } = render(<AreaChart {...props({ formatValue: (v) => `${v} tickets` })} />)
    expect(legendItems(container)).toEqual([
      ['Created', '12 tickets'],
      ['Resolved', '5 tickets'],
    ])
  })

  it('shows the totals in the empty state too, including a zero total', () => {
    const { container } = render(
      <AreaChart
        {...props({
          series: [
            { key: 'created', label: 'Created', values: [0, 0, 0], tone: 'blue' },
            { key: 'resolved', label: 'Resolved', values: [0, 0, 0], tone: 'green' },
          ],
          emptyText: 'None.',
        })}
      />,
    )
    expect(container.querySelector('svg')).toBeNull()
    expect(legendItems(container)).toEqual([
      ['Created', '0'],
      ['Resolved', '0'],
    ])
  })

  it('shows a zero total for a series that is zero while another is not', () => {
    const { container } = render(
      <AreaChart
        {...props({
          series: [
            { key: 'created', label: 'Created', values: [1, 2, 3], tone: 'blue' },
            { key: 'resolved', label: 'Resolved', values: [0, 0, 0], tone: 'green' },
          ],
        })}
      />,
    )
    expect(legendItems(container)).toEqual([
      ['Created', '6'],
      ['Resolved', '0'],
    ])
  })
})

describe('AreaChart: empty', () => {
  it('shows emptyText instead of the marks when every value is 0', () => {
    const { container } = render(
      <AreaChart
        {...props({
          series: [{ key: 'created', label: 'Created', values: [0, 0, 0], tone: 'blue' }],
          emptyText: 'No tickets this month.',
        })}
      />,
    )
    expect(container.querySelector('svg')).toBeNull()
    expect(container.textContent).toContain('No tickets this month.')
  })

  it('shows emptyText when there are no labels', () => {
    const { container } = render(
      <AreaChart
        {...props({
          labels: [],
          series: [{ key: 'c', label: 'C', values: [], tone: 'gray' }],
          emptyText: 'Nothing.',
        })}
      />,
    )
    expect(container.querySelector('svg')).toBeNull()
    expect(container.textContent).toContain('Nothing.')
  })
})

describe('AreaChart: programming errors', () => {
  it('throws when a series length differs from the labels', () => {
    expect(() =>
      render(<AreaChart {...props({ series: [{ key: 'c', label: 'C', values: [1, 2], tone: 'blue' }] })} />),
    ).toThrow(/labels/)
  })
})
