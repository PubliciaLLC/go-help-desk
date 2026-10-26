import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import type { Ticket } from '@/api/types'

// How the ticket arrived (#130): a small "via ..." line on the ticket page for
// staff and administrators. The server does not send the value to a requester,
// and the page would not draw it if it did.

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

function mockApi(t: Partial<Ticket>) {
  const ticket: Ticket = {
    id: TICKET_ID,
    tracking_number: 'TKT-0001',
    subject: 'Printer is on fire',
    description: 'Literally.',
    description_format: 'plain',
    category_id: 'cat-1',
    priority: 'high',
    status_id: 'st-new',
    created_at: '2026-09-20T10:00:00Z',
    updated_at: '2026-09-20T10:00:00Z',
    ...t,
  }
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === `/tickets/${TICKET_ID}`) return Promise.resolve({ data: ticket })
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

const LINE = 'source-channel-line'

beforeEach(() => {
  vi.restoreAllMocks()
  signIn('staff')
})

describe('the "via" line', () => {
  it.each([
    ['web', 'via web'],
    ['api', 'via API'],
    ['mcp', 'via MCP'],
    ['guest', 'via guest form'],
    ['phone', 'via phone'],
  ] as const)('reads "%s" as "%s" for staff', async (channel, text) => {
    mockApi({ source_channel: channel })
    renderWithQuery(<TicketDetailPage />)
    expect((await screen.findByTestId(LINE)).textContent).toBe(text)
  })

  it('is shown to an administrator', async () => {
    signIn('admin')
    mockApi({ source_channel: 'phone' })
    renderWithQuery(<TicketDetailPage />)
    expect((await screen.findByTestId(LINE)).textContent).toBe('via phone')
  })

  it('is not drawn for a requester, even if the value were sent', async () => {
    signIn('user')
    mockApi({ source_channel: 'phone' })
    renderWithQuery(<TicketDetailPage />)
    await screen.findByText('Printer is on fire')
    await waitFor(() => expect(screen.queryByTestId(LINE)).toBeNull())
  })

  it('is not drawn when the ticket carries no channel', async () => {
    mockApi({})
    renderWithQuery(<TicketDetailPage />)
    await screen.findByText('Printer is on fire')
    expect(screen.queryByTestId(LINE)).toBeNull()
  })
})
