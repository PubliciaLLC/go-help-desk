import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #625: what the ticket page does with a closed ticket that already has a
// follow-up, and with staff who may still reply to a closed ticket.

const TICKET_ID = 'tkt-1'

vi.mock('@tanstack/react-router', () => ({
  useParams: () => ({ id: TICKET_ID }),
  useRouterState: () => ({ location: { pathname: `/tickets/${TICKET_ID}` } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, params, children, ...rest }: any) => (
    <a href={params?.id ? to.replace('$id', params.id) : to} {...rest}>
      {children}
    </a>
  ),
}))

import { TicketDetailPage } from './TicketDetailPage'

const STATUSES = [
  { id: 'st-new', name: 'New', kind: 'system', sort_order: 1, color: '#888', active: true, ticket_count: 1 },
  { id: 'st-closed', name: 'Closed', kind: 'system', sort_order: 3, color: '#999', active: true, ticket_count: 1 },
]

const FOLLOW_UP = { id: 'tkt-2', tracking_number: 'TKT-0002' }

function closedTicket(extra: Record<string, unknown> = {}) {
  return {
    id: TICKET_ID,
    tracking_number: 'TKT-0001',
    subject: 'Printer is on fire',
    description: 'Literally.',
    category_id: 'cat-1',
    priority: 'critical',
    status_id: 'st-closed',
    created_at: '2026-09-20T10:00:00Z',
    updated_at: '2026-09-20T10:00:00Z',
    sla: null,
    ...extra,
  }
}

function mockApiGet(ticket: Record<string, unknown>) {
  return vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === `/tickets/${TICKET_ID}`) return Promise.resolve({ data: ticket })
    if (url === '/statuses') return Promise.resolve({ data: STATUSES })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

function signInAs(role: 'staff' | 'admin' | 'user') {
  useAuthStore.setState({
    user: {
      id: 'u-1',
      email: `${role}@example.com`,
      display_name: 'Sam',
      role,
      mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })
}

async function renderTicket(ticket: Record<string, unknown>) {
  mockApiGet(ticket)
  renderWithQuery(<TicketDetailPage />)
  await waitFor(() => expect(document.body.textContent).toContain('Printer is on fire'))
}

beforeEach(() => signInAs('staff'))

describe('a closed ticket that already has a follow-up', () => {
  it('shows a requester the link and no button that would be refused', async () => {
    signInAs('user')
    await renderTicket(closedTicket({ follow_up: FOLLOW_UP }))

    expect(screen.queryByRole('button', { name: 'Create follow-up' })).toBeNull()
    const link = screen.getByRole('link', { name: 'TKT-0002' })
    expect(link.getAttribute('href')).toBe('/tickets/tkt-2')
    expect(screen.getByRole('note').textContent).not.toMatch(/use .Create follow-up./i)
  })

  it('still offers a requester the button when there is none', async () => {
    signInAs('user')
    await renderTicket(closedTicket())
    expect(screen.queryByRole('button', { name: 'Create follow-up' })).not.toBeNull()
    expect(screen.queryByRole('link', { name: 'TKT-0002' })).toBeNull()
  })

  it('shows staff the link and keeps the button: staff are not limited', async () => {
    for (const role of ['staff', 'admin'] as const) {
      signInAs(role)
      mockApiGet(closedTicket({ follow_up: FOLLOW_UP }))
      const { unmount } = renderWithQuery(<TicketDetailPage />)
      await waitFor(() => expect(screen.queryByRole('link', { name: 'TKT-0002' })).not.toBeNull())
      expect(screen.queryByRole('button', { name: 'Create follow-up' })).not.toBeNull()
      unmount()
    }
  })

  it('says one follow-up is made once, not twice, right after creating it', async () => {
    signInAs('user')
    await renderTicket(closedTicket())
    vi.spyOn(api, 'post').mockResolvedValue({ data: { id: 'tkt-9', tracking_number: 'TKT-0009' } } as never)

    await userEvent.click(screen.getByRole('button', { name: 'Create follow-up' }))

    await waitFor(() => expect(screen.getByRole('status').textContent).toContain('TKT-0009'))
    expect(screen.getAllByRole('status')).toHaveLength(1)
  })
})

describe('the reply composer on a closed ticket', () => {
  it('is shown to staff and admin, with the words that it does not reopen the ticket', async () => {
    for (const role of ['staff', 'admin'] as const) {
      signInAs(role)
      mockApiGet(closedTicket())
      const { unmount } = renderWithQuery(<TicketDetailPage />)
      await waitFor(() => expect(screen.queryByPlaceholderText(/describe the work performed/i)).not.toBeNull())
      expect(document.body.textContent).toContain('Replying does not reopen this ticket')
      unmount()
    }
  })

  it('stays hidden from a requester', async () => {
    signInAs('user')
    await renderTicket(closedTicket())
    expect(screen.queryByPlaceholderText(/type your reply/i)).toBeNull()
    expect(document.body.textContent).not.toContain('Replying does not reopen this ticket')
  })

  it('does not say it on a ticket that is open', async () => {
    mockApiGet(closedTicket({ status_id: 'st-new' }))
    renderWithQuery(<TicketDetailPage />)
    await waitFor(() => expect(screen.queryByPlaceholderText(/describe the work performed/i)).not.toBeNull())
    expect(document.body.textContent).not.toContain('Replying does not reopen this ticket')
  })
})
