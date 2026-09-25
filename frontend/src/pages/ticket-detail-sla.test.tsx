import { describe, expect, it, vi, beforeEach } from 'vitest'
import { waitFor } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// The SLA indicator in the ticket header (#183, docs/DESIGN.md → SLA
// Tracking → SLA Indicators): two outline pills after the priority badge,
// one per target, each titled for its own target so a reader (or a test)
// can tell which is which without relying on layout order.

const TICKET_ID = 'tkt-1'

vi.mock('@tanstack/react-router', () => ({
  useParams: () => ({ id: TICKET_ID }),
  useRouterState: () => ({ location: { pathname: `/tickets/${TICKET_ID}` } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}))

import { TicketDetailPage } from './TicketDetailPage'

const STATUSES = [
  { id: 'st-new', name: 'New', kind: 'system', sort_order: 1, color: '#888', active: true, ticket_count: 1 },
]

const TICKET = {
  id: TICKET_ID,
  tracking_number: 'TKT-0001',
  subject: 'Printer is on fire',
  description: 'Literally.',
  category_id: 'cat-1',
  priority: 'critical',
  status_id: 'st-new',
  created_at: '2026-09-20T10:00:00Z',
  updated_at: '2026-09-20T10:00:00Z',
  sla: {
    policy_id: 'pol-1',
    policy_name: 'Critical — 1h response',
    response: { color: 'red', target_min: 60, elapsed_min: 80, remaining_min: -20, met_at: null },
    resolution: { color: 'green', target_min: 480, elapsed_min: 80, remaining_min: 400, met_at: null },
  },
}

function mockApi(ticket: typeof TICKET) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === `/tickets/${TICKET_ID}`) return Promise.resolve({ data: ticket })
    if (url === `/tickets/${TICKET_ID}/attachments`) return Promise.resolve({ data: [] })
    if (url === `/tickets/${TICKET_ID}/replies`) return Promise.resolve({ data: [] })
    if (url === `/tickets/${TICKET_ID}/history`) return Promise.resolve({ data: [] })
    if (url === `/tickets/${TICKET_ID}/links`) return Promise.resolve({ data: [] })
    if (url === '/statuses') return Promise.resolve({ data: STATUSES })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

beforeEach(() => {
  useAuthStore.setState({
    user: {
      id: 'u-1',
      email: 'staff@example.com',
      display_name: 'Sam Staff',
      role: 'staff',
      mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })
})

describe('the SLA indicator on the ticket header', () => {
  it('shows a red, over-target response pill and a green, on-track resolution pill', async () => {
    mockApi(TICKET)
    renderWithQuery(<TicketDetailPage />)

    await waitFor(() => {
      expect(document.body.textContent).toContain(TICKET.subject)
    })

    const response = document.querySelector('[data-target="response"]')
    expect(response).not.toBeNull()
    expect(response?.getAttribute('data-color')).toBe('red')
    expect(response?.getAttribute('title')).toMatch(/^Response SLA: .*over/)

    const resolution = document.querySelector('[data-target="resolution"]')
    expect(resolution).not.toBeNull()
    expect(resolution?.getAttribute('data-color')).toBe('green')
    expect(resolution?.getAttribute('title')).toMatch(/^Resolution SLA: .*left/)
  })

  it('shows nothing when the ticket has no matching policy', async () => {
    mockApi({ ...TICKET, sla: null } as unknown as typeof TICKET)
    renderWithQuery(<TicketDetailPage />)

    await waitFor(() => {
      expect(document.body.textContent).toContain(TICKET.subject)
    })

    expect(document.querySelectorAll('[data-target]').length).toBe(0)
  })
})
