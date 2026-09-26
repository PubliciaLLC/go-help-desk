import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'

import { SLAIndicator } from './SLAIndicator'
import type { SLAColor, TicketSLA } from '@/api/types'

// sla is a plain prop with no query behind it, so a bare render() is enough —
// no need for renderWithQuery's QueryClientProvider.

function target(color: SLAColor, over: Partial<TicketSLA['response']> = {}) {
  return {
    color,
    target_min: 60,
    elapsed_min: 12,
    remaining_min: 48,
    met_at: null,
    ...over,
  }
}

function sla(over: Partial<TicketSLA> = {}): TicketSLA {
  return {
    policy_id: 'pol-1',
    policy_name: 'Standard',
    response: target('green'),
    resolution: target('green'),
    ...over,
  }
}

describe('SLAIndicator', () => {
  it('renders nothing when sla is null', () => {
    const { container } = render(<SLAIndicator sla={null} />)
    expect(container.firstChild).toBeNull()
  })

  it('renders nothing when sla is undefined', () => {
    const { container } = render(<SLAIndicator sla={undefined} />)
    expect(container.firstChild).toBeNull()
  })

  it.each<SLAColor>(['green', 'amber', 'red'])('renders a %s response target', (color) => {
    const { container } = render(<SLAIndicator sla={sla({ response: target(color) })} />)
    const el = container.querySelector('[data-target="response"]')
    expect(el).not.toBeNull()
    expect(el?.getAttribute('data-color')).toBe(color)
  })

  it('renders response and resolution as two independently coloured elements', () => {
    const { container } = render(
      <SLAIndicator
        sla={sla({
          response: target('red', { remaining_min: -20 }),
          resolution: target('amber'),
        })}
      />,
    )

    const response = container.querySelector('[data-target="response"]')
    const resolution = container.querySelector('[data-target="resolution"]')
    expect(response?.getAttribute('data-color')).toBe('red')
    expect(resolution?.getAttribute('data-color')).toBe('amber')
    expect(response).not.toBe(resolution)
  })

  it('marks a met target and titles it starting with "Met"', () => {
    const { container } = render(
      <SLAIndicator
        sla={sla({ response: target('green', { met_at: '2026-09-25T10:12:00Z', remaining_min: 12 }) })}
      />,
    )
    const el = container.querySelector('[data-target="response"]')
    expect(el?.getAttribute('data-met')).toBe('true')
    expect(el?.getAttribute('title')).toMatch(/^Response SLA: Met/)
  })

  it('leaves an outstanding target unmarked', () => {
    const { container } = render(<SLAIndicator sla={sla()} />)
    const el = container.querySelector('[data-target="resolution"]')
    expect(el?.getAttribute('data-met')).toBe('false')
  })

  it('compact mode renders no visible text', () => {
    const { container } = render(<SLAIndicator sla={sla()} compact />)
    expect(container.textContent).toBe('')
  })

  it('the full (non-compact) mode does show label text', () => {
    const { container } = render(<SLAIndicator sla={sla()} />)
    expect(container.textContent).toMatch(/Response/)
    expect(container.textContent).toMatch(/Resolution/)
  })
})
