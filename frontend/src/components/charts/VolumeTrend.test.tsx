import { describe, expect, it } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import type { DashboardVolume, VolumeDay } from '@/api/types'
import { VolumeTrend } from './VolumeTrend'

// 30 days ending 2026-11-24, oldest first: 2026-10-26 .. 2026-11-24.
function days(f: (i: number) => { created: number; resolved: number }): VolumeDay[] {
  return Array.from({ length: 30 }, (_, i) => {
    const d = new Date(Date.UTC(2026, 9, 26 + i))
    return { date: d.toISOString().slice(0, 10), ...f(i) }
  })
}

const volume = (d: VolumeDay[]): DashboardVolume => ({ tz: 'Europe/London', days: d })

describe('VolumeTrend', () => {
  it('draws Created in blue and Resolved in green over the 30 days', () => {
    const { container } = render(<VolumeTrend volume={volume(days((i) => ({ created: i, resolved: i / 2 })))} />)
    const svg = container.querySelector('svg')!
    expect(svg.getAttribute('aria-label')).toBe('Tickets created and resolved per day, last 30 days')
    const groups = Array.from(svg.querySelectorAll('g'))
    expect(groups).toHaveLength(2)
    expect(groups[0].getAttribute('class')).toContain('text-chart-blue')
    expect(groups[1].getAttribute('class')).toContain('text-chart-green')
  })

  it('shows each series 30-day total in the legend', () => {
    // created: 2 a day = 60; resolved: 1 a day = 30.
    render(<VolumeTrend volume={volume(days(() => ({ created: 2, resolved: 1 })))} />)
    const legend = screen.getByRole('list')
    const created = within(legend).getByText('Created').closest('li')!
    const resolved = within(legend).getByText('Resolved').closest('li')!
    expect(within(created).getByText('60')).toBeTruthy()
    expect(within(resolved).getByText('30')).toBeTruthy()
  })

  it('labels the first and last day as day and month, from the date alone', () => {
    const { container } = render(<VolumeTrend volume={volume(days(() => ({ created: 1, resolved: 0 })))} />)
    // The first and last labels under the chart, and the table's rows.
    expect(screen.getAllByText('26 Oct').length).toBeGreaterThanOrEqual(2)
    expect(screen.getAllByText('24 Nov').length).toBeGreaterThanOrEqual(2)
    expect(container.querySelectorAll('tbody tr')).toHaveLength(30)
  })

  it('formats a single-digit day and September without padding or a long month', () => {
    const d = days(() => ({ created: 1, resolved: 0 }))
    d[0].date = '2026-09-05'
    render(<VolumeTrend volume={volume(d)} />)
    expect(screen.getAllByText('5 Sep').length).toBeGreaterThanOrEqual(1)
  })

  it('keeps its box and says so when every day is zero', () => {
    const { container } = render(<VolumeTrend volume={volume(days(() => ({ created: 0, resolved: 0 })))} />)
    expect(screen.getByText('No tickets were created or resolved in the last 30 days.')).toBeTruthy()
    expect(container.querySelector('svg')).toBeNull()
    // The legend still has its totals.
    const created = screen.getByText('Created').closest('li')!
    expect(within(created).getByText('0')).toBeTruthy()
  })

  it('is empty with no days at all', () => {
    render(<VolumeTrend volume={volume([])} />)
    expect(screen.getByText('No tickets were created or resolved in the last 30 days.')).toBeTruthy()
  })

  it('says what the caller says when it passes its own empty text', () => {
    render(
      <VolumeTrend
        volume={volume(days(() => ({ created: 0, resolved: 0 })))}
        emptyText="No tickets were created or resolved in this period."
      />,
    )
    expect(screen.getByText('No tickets were created or resolved in this period.')).toBeTruthy()
    expect(screen.queryByText('No tickets were created or resolved in the last 30 days.')).toBeNull()
  })

  it('labels the chart with the caller wording when it passes its own', () => {
    const { container } = render(
      <VolumeTrend
        volume={volume(days(() => ({ created: 1, resolved: 1 })))}
        ariaLabel="Tickets created and resolved per day, 1 Oct to 30 Oct"
      />,
    )
    expect(container.querySelector('svg')!.getAttribute('aria-label')).toBe(
      'Tickets created and resolved per day, 1 Oct to 30 Oct',
    )
  })
})
