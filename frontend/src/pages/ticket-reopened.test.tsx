import { describe, expect, it, vi, beforeEach } from 'vitest'
import { waitFor } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import type { Ticket } from '@/api/types'

// The "Reopened" badge (#464) on the ticket page and in the queue, for staff
// and administrators only, while the ticket is open.

const TICKET_ID = 'tkt-1'

vi.mock('@tanstack/react-router', () => ({
  useParams: () => ({ id: TICKET_ID }),
  useNavigate: () => vi.fn(),
  useSearch: () => ({ status: undefined, reporter: undefined }),
  useRouterState: () => ({ location: { pathname: `/tickets/${TICKET_ID}` } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}))

import { TicketDetailPage } from './TicketDetailPage'
import { TicketListPage } from './TicketListPage'

const STATUSES = [
  { id: 'st-new', name: 'New', kind: 'system', sort_order: 1, color: '#888', active: true, ticket_count: 1 },
  { id: 'st-res', name: 'Resolved', kind: 'system', sort_order: 2, color: '#0a0', active: true, ticket_count: 1 },
]

function ticket(over: Partial<Ticket>): Ticket {
  return {
    id: TICKET_ID,
    tracking_number: 'TKT-0001',
    subject: 'Printer is on fire',
    description: 'Literally.',
    category_id: 'cat-1',
    priority: 'high',
    status_id: 'st-new',
    created_at: '2026-09-20T10:00:00Z',
    updated_at: '2026-09-20T10:00:00Z',
    ...over,
  }
}

function mockApi(tickets: Ticket[]) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === `/tickets/${TICKET_ID}`) return Promise.resolve({ data: tickets[0] })
    if (url === '/tickets') return Promise.resolve({ data: tickets })
    if (url === '/statuses') return Promise.resolve({ data: STATUSES })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

function signIn(role: 'staff' | 'user') {
  useAuthStore.setState({
    user: {
      id: 'u-1',
      email: 'someone@example.com',
      display_name: 'Someone',
      role,
      mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })
}

const BADGE = '[data-testid="reopened-badge"]'

beforeEach(() => signIn('staff'))

describe('the Reopened badge on the ticket page', () => {
  it('shows for staff on an open ticket that has been reopened', async () => {
    mockApi([ticket({ reopen_count: 1 })])
    renderWithQuery(<TicketDetailPage />)
    await waitFor(() => expect(document.body.textContent).toContain('Printer is on fire'))
    await waitFor(() => expect(document.querySelector(BADGE)).not.toBeNull())
  })

  it('is absent for a ticket that was never reopened', async () => {
    mockApi([ticket({ reopen_count: 0 })])
    renderWithQuery(<TicketDetailPage />)
    await waitFor(() => expect(document.body.textContent).toContain('Printer is on fire'))
    expect(document.querySelector(BADGE)).toBeNull()
  })

  it('is gone once the ticket is resolved again, though the count stays', async () => {
    mockApi([ticket({ reopen_count: 2, status_id: 'st-res' })])
    renderWithQuery(<TicketDetailPage />)
    await waitFor(() => expect(document.body.textContent).toContain('Printer is on fire'))
    await waitFor(() => expect(document.body.textContent).toContain('Resolved'))
    expect(document.querySelector(BADGE)).toBeNull()
  })

  it('is not shown to the requester', async () => {
    signIn('user')
    mockApi([ticket({ reopen_count: 1 })])
    renderWithQuery(<TicketDetailPage />)
    await waitFor(() => expect(document.body.textContent).toContain('Printer is on fire'))
    expect(document.querySelector(BADGE)).toBeNull()
  })
})

describe('the Reopened badge in the queue', () => {
  it('marks a reopened open ticket and no other, in the table and in the mobile cards', async () => {
    mockApi([
      ticket({ id: 'a', subject: 'Reopened one', reopen_count: 1 }),
      ticket({ id: 'b', subject: 'Plain one', reopen_count: 0 }),
      ticket({ id: 'c', subject: 'Reopened and resolved again', reopen_count: 1, status_id: 'st-res' }),
    ])
    renderWithQuery(<TicketListPage />)
    await waitFor(() => expect(document.body.textContent).toContain('Reopened one'))

    const rows = Array.from(document.querySelectorAll('tbody tr'))
    const row = (s: string) => rows.find((r) => r.textContent?.includes(s))!
    expect(row('Reopened one').querySelector(BADGE)).not.toBeNull()
    expect(row('Plain one').querySelector(BADGE)).toBeNull()
    expect(row('Reopened and resolved again').querySelector(BADGE)).toBeNull()

    const cards = Array.from(document.querySelectorAll('ul > li'))
    const card = (s: string) => cards.find((c) => c.textContent?.includes(s))!
    expect(card('Reopened one').querySelector(BADGE)).not.toBeNull()
    expect(card('Plain one').querySelector(BADGE)).toBeNull()
  })

  it('is not shown to a requester', async () => {
    signIn('user')
    mockApi([ticket({ id: 'a', subject: 'Reopened one', reopen_count: 1 })])
    renderWithQuery(<TicketListPage />)
    await waitFor(() => expect(document.body.textContent).toContain('Reopened one'))
    expect(document.querySelector(BADGE)).toBeNull()
  })
})
