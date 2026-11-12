import { describe, expect, it, vi, beforeEach } from 'vitest'
import { waitFor } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// Requesters see the current state of their ticket and its conversation, not
// its history (Erik, 2026-10-15): no status-change events in the thread, no
// activity panel, and the page does not even ask for either, since the API
// refuses a requester both.

const TICKET_ID = 'tkt-1'

vi.mock('@tanstack/react-router', () => ({
  useParams: () => ({ id: TICKET_ID }),
  useNavigate: () => vi.fn(),
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
  { id: 'st-prog', name: 'In Progress', kind: 'custom', sort_order: 2, color: '#fa0', active: true, ticket_count: 0 },
]

const TICKET = {
  id: TICKET_ID,
  tracking_number: 'TKT-0001',
  subject: 'Printer is on fire',
  description: 'Literally.',
  category_id: 'cat-1',
  priority: 'high',
  status_id: 'st-prog',
  created_at: '2026-09-20T10:00:00Z',
  updated_at: '2026-09-20T10:00:00Z',
}

const HISTORY = [
  {
    id: 'h1', ticket_id: TICKET_ID, from_status_id: 'st-new', from_status_name: 'New', from_status_color: '#888',
    to_status_id: 'st-prog', to_status_name: 'In Progress', to_status_color: '#fa0',
    changed_by_name: 'Sam Staff', created_at: '2026-09-21T10:00:00Z',
  },
]

const REPLIES = [
  { id: 'r1', ticket_id: TICKET_ID, author_name: 'Sam Staff', body: 'We are on it', internal: false, created_at: '2026-09-21T11:00:00Z' },
]

let requested: string[] = []

function mockApi() {
  requested = []
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    requested.push(url)
    if (url === `/tickets/${TICKET_ID}`) return Promise.resolve({ data: TICKET })
    if (url === `/tickets/${TICKET_ID}/history`) return Promise.resolve({ data: HISTORY })
    if (url === `/tickets/${TICKET_ID}/replies`) return Promise.resolve({ data: REPLIES })
    if (url === '/statuses') return Promise.resolve({ data: STATUSES })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

function signIn(role: 'staff' | 'user') {
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'someone@example.com', display_name: 'Someone', role, mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
}

beforeEach(() => mockApi())

describe('ticket history on the ticket page', () => {
  it('is hidden from a requester: no status events, no activity panel, no request for either', async () => {
    signIn('user')
    renderWithQuery(<TicketDetailPage />)
    await waitFor(() => expect(document.body.textContent).toContain('We are on it'))

    expect(document.body.textContent).not.toContain('Ticket opened as')
    expect(document.body.textContent).not.toMatch(/New\s*→\s*In Progress/)
    expect(document.body.textContent).not.toMatch(/Activity/i)
    expect(requested).not.toContain(`/tickets/${TICKET_ID}/history`)
    expect(requested).not.toContain(`/tickets/${TICKET_ID}/audit`)
  })

  it('stays for staff: the status change shows in the thread', async () => {
    signIn('staff')
    renderWithQuery(<TicketDetailPage />)
    await waitFor(() => expect(document.body.textContent).toMatch(/New\s*→\s*In Progress/))
    expect(requested).toContain(`/tickets/${TICKET_ID}/history`)
  })
})
