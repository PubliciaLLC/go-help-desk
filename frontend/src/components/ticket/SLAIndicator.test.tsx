import { describe, expect, it } from 'vitest'
import { fireEvent, render } from '@testing-library/react'

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

  describe('the "SLA restarted" badge (#443)', () => {
    const earlier = {
      policy_id: 'pol-0',
      policy_name: 'Standard medium',
      started_at: '2026-10-01T09:00:00Z',
      superseded_at: '2026-10-02T09:00:00Z',
      outcome: 'breached' as const,
    }

    it('is not shown without an earlier record', () => {
      const { container } = render(<SLAIndicator sla={sla()} />)
      expect(container.textContent).not.toMatch(/SLA restarted/)
      expect(container.querySelector('[data-testid="sla-restarted"]')).toBeNull()
    })

    it('is shown, with an explanatory title, when the SLA has an earlier record', () => {
      const { container } = render(<SLAIndicator sla={sla({ earlier })} />)
      const badge = container.querySelector('[data-testid="sla-restarted"]')
      expect(badge).not.toBeNull()
      expect(badge?.textContent).toBe('SLA restarted')
      expect(badge?.getAttribute('title')).toBe(
        'The SLA clock restarted when this ticket was reopened. The earlier record is kept; select to see it.',
      )
    })

    it('opens a panel with the earlier record\'s facts, and closes it again', () => {
      const { container } = render(<SLAIndicator sla={sla({ earlier })} />)
      expect(container.querySelector('[data-testid="sla-earlier"]')).toBeNull()

      const badge = container.querySelector('[data-testid="sla-restarted"]') as HTMLElement
      fireEvent.click(badge)
      const panel = container.querySelector('[data-testid="sla-earlier"]')
      expect(panel).not.toBeNull()
      expect(badge.getAttribute('aria-expanded')).toBe('true')
      expect(panel?.textContent).toContain('Standard medium')
      expect(panel?.textContent).toMatch(/breached/i)
      expect(panel?.textContent).toMatch(/started/i)
      expect(panel?.textContent).toMatch(/replaced/i)

      fireEvent.click(badge)
      expect(container.querySelector('[data-testid="sla-earlier"]')).toBeNull()
    })

    it('closes the panel on Escape and returns focus to the badge', () => {
      const { container } = render(<SLAIndicator sla={sla({ earlier })} />)
      const badge = container.querySelector('[data-testid="sla-restarted"]') as HTMLElement
      fireEvent.click(badge)
      expect(container.querySelector('[data-testid="sla-earlier"]')).not.toBeNull()

      fireEvent.keyDown(document.body, { key: 'Escape' })
      expect(container.querySelector('[data-testid="sla-earlier"]')).toBeNull()
      expect(badge.getAttribute('aria-expanded')).toBe('false')
      expect(document.activeElement).toBe(badge)
    })

    it('ignores other keys, and Escape while the panel is closed does not take focus', () => {
      const { container } = render(<SLAIndicator sla={sla({ earlier })} />)
      const badge = container.querySelector('[data-testid="sla-restarted"]') as HTMLElement

      fireEvent.keyDown(document.body, { key: 'Escape' })
      expect(document.activeElement).not.toBe(badge)

      fireEvent.click(badge)
      fireEvent.keyDown(document.body, { key: 'Enter' })
      expect(container.querySelector('[data-testid="sla-earlier"]')).not.toBeNull()
    })

    it('stops listening for Escape once it is unmounted', () => {
      const { container, unmount } = render(<SLAIndicator sla={sla({ earlier })} />)
      fireEvent.click(container.querySelector('[data-testid="sla-restarted"]') as HTMLElement)
      unmount()
      expect(() => fireEvent.keyDown(document.body, { key: 'Escape' })).not.toThrow()
    })

    it('shows a met outcome as met', () => {
      const { container } = render(<SLAIndicator sla={sla({ earlier: { ...earlier, outcome: 'met' } })} />)
      fireEvent.click(container.querySelector('[data-testid="sla-restarted"]') as HTMLElement)
      expect(container.querySelector('[data-testid="sla-earlier"]')?.textContent).toMatch(/met/i)
      expect(container.querySelector('[data-testid="sla-earlier"]')?.textContent).not.toMatch(/breached/i)
    })

    it('is not part of the compact (queue) form', () => {
      const { container } = render(<SLAIndicator sla={sla({ earlier })} compact />)
      expect(container.querySelector('[data-testid="sla-restarted"]')).toBeNull()
    })
  })
})
