import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import type { Ticket } from '@/api/types'

// The Requester line (#444) on the staff ticket page: who the ticket is for,
// the account's name and address or the guest's. Staff and administrators only.

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

const STATUSES = [
  { id: 'st-new', name: 'New', kind: 'system', sort_order: 1, color: '#888', active: true, ticket_count: 1 },
]

function ticket(over: Partial<Ticket>): Ticket {
  return {
    id: TICKET_ID,
    tracking_number: 'TKT-0001',
    subject: 'Printer is on fire',
    description: 'Literally.',
    description_format: 'plain',
    category_id: 'cat-1',
    priority: 'high',
    status_id: 'st-new',
    created_at: '2026-09-20T10:00:00Z',
    ...over,
  }
}

function mockApi(t: Ticket) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === `/tickets/${TICKET_ID}`) return Promise.resolve({ data: t })
    if (url === '/statuses') return Promise.resolve({ data: STATUSES })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

function signIn(role: 'staff' | 'admin' | 'user') {
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

const LINE = '[data-testid="requester-line"]'

beforeEach(() => vi.restoreAllMocks())

describe('the Requester line on the ticket page', () => {
  it.each(['staff', 'admin'] as const)('shows %s the requester\'s name and address', async (role) => {
    signIn(role)
    mockApi(ticket({ requester: { name: 'Ada Lovelace', email: 'ada@example.com' } }))
    renderWithQuery(<TicketDetailPage />)

    await waitFor(() => expect(document.querySelector(LINE)).not.toBeNull())
    const line = document.querySelector(LINE)!.textContent!
    expect(line).toContain('Requester')
    expect(line).toContain('Ada Lovelace')
    expect(line).toContain('ada@example.com')
  })

  it('shows a guest requester the same way', async () => {
    signIn('staff')
    mockApi(ticket({ guest_email: 'g@example.com', requester: { name: 'Guest Gail', email: 'g@example.com' } }))
    renderWithQuery(<TicketDetailPage />)

    await waitFor(() => expect(document.querySelector(LINE)).not.toBeNull())
    expect(document.querySelector(LINE)!.textContent).toContain('Guest Gail')
  })

  it('says so when the server could not name the requester', async () => {
    signIn('staff')
    mockApi(ticket({}))
    renderWithQuery(<TicketDetailPage />)

    await waitFor(() => expect(document.querySelector(LINE)).not.toBeNull())
    expect(document.querySelector(LINE)!.textContent).toMatch(/unknown/i)
  })

  it('is not shown to a requester, whatever the response carries', async () => {
    signIn('user')
    mockApi(ticket({ requester: { name: 'Ada Lovelace', email: 'ada@example.com' } }))
    renderWithQuery(<TicketDetailPage />)

    await screen.findByText('Printer is on fire')
    expect(document.querySelector(LINE)).toBeNull()
    expect(document.body.textContent).not.toContain('Requester')
  })
})
