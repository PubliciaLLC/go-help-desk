import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #349: Closed is terminal and archived read-only. Nobody reopens a closed
// ticket (this file used to pin the Reopen button, #277), a requester can read
// it and change nothing, and staff and admin continue the work in a new,
// linked follow-up ticket instead.

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
  { id: 'st-resolved', name: 'Resolved', kind: 'system', sort_order: 2, color: '#4caf50', active: true, ticket_count: 1 },
  { id: 'st-closed', name: 'Closed', kind: 'system', sort_order: 3, color: '#999', active: true, ticket_count: 1 },
]

function ticketWithStatus(statusId: string) {
  return {
    id: TICKET_ID,
    tracking_number: 'TKT-0001',
    subject: 'Printer is on fire',
    description: 'Literally.',
    category_id: 'cat-1',
    priority: 'critical',
    status_id: statusId,
    created_at: '2026-09-20T10:00:00Z',
    updated_at: '2026-09-20T10:00:00Z',
    sla: null,
  }
}

function mockApiGet(ticket: ReturnType<typeof ticketWithStatus>) {
  return vi.spyOn(api, 'get').mockImplementation(((url: string) => {
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

beforeEach(() => {
  signInAs('staff')
})

async function renderTicket(ticket: ReturnType<typeof ticketWithStatus>) {
  const getSpy = mockApiGet(ticket)
  renderWithQuery(<TicketDetailPage />)
  await waitFor(() => {
    expect(document.body.textContent).toContain('Printer is on fire')
  })
  return getSpy
}

describe('a closed ticket', () => {
  it('has no Reopen button for staff: nothing leaves Closed', async () => {
    await renderTicket(ticketWithStatus('st-closed'))
    expect(screen.queryByRole('button', { name: 'Reopen' })).toBeNull()
  })

  it('offers staff and admin a follow-up instead, and no way to change its status or reply', async () => {
    for (const role of ['staff', 'admin'] as const) {
      signInAs(role)
      mockApiGet(ticketWithStatus('st-closed'))
      const { unmount } = renderWithQuery(<TicketDetailPage />)
      await waitFor(() => {
        expect(screen.queryByRole('button', { name: 'Create follow-up' })).not.toBeNull()
      })
      // The status picker would only offer moves the server refuses.
      expect(screen.queryByRole('combobox', { name: 'Ticket status' })).toBeNull()
      expect(screen.queryByPlaceholderText(/describe the work performed/i)).toBeNull()
      expect(screen.getByRole('note').textContent).toMatch(/cannot be reopened/i)
      unmount()
    }
  })

  it('creates the follow-up and links to it', async () => {
    await renderTicket(ticketWithStatus('st-closed'))
    const post = vi.spyOn(api, 'post').mockResolvedValue({
      data: { id: 'tkt-2', tracking_number: 'TKT-0002' },
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any)

    await userEvent.click(screen.getByRole('button', { name: 'Create follow-up' }))

    await waitFor(() => expect(screen.getByRole('status').textContent).toContain('TKT-0002'))
    expect(post).toHaveBeenCalledWith(`/tickets/${TICKET_ID}/follow-up`, {})
    // Once is enough: a second click would open a second ticket.
    expect((screen.getByRole('button', { name: 'Create follow-up' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('shows the server message when the follow-up is refused', async () => {
    await renderTicket(ticketWithStatus('st-closed'))
    vi.spyOn(api, 'post').mockRejectedValue({
      isAxiosError: true,
      response: {
        data: { error: { code: 'ticket_not_closed', message: 'only a closed ticket can have a follow-up' } },
      },
    })

    await userEvent.click(screen.getByRole('button', { name: 'Create follow-up' }))

    await waitFor(() => {
      expect(screen.getByRole('alert').textContent).toContain('only a closed ticket can have a follow-up')
    })
  })

  it('is read-only for a requester: no reply box, no follow-up, and it says why', async () => {
    signInAs('user')
    await renderTicket(ticketWithStatus('st-closed'))

    expect(screen.queryByRole('button', { name: 'Create follow-up' })).toBeNull()
    expect(screen.queryByPlaceholderText(/type your reply/i)).toBeNull()
    expect(screen.getByRole('note').textContent).toMatch(/closed and read-only/i)
    // They can still read it.
    expect(document.body.textContent).toContain('Literally.')
  })
})

describe('a ticket that is not closed', () => {
  it('shows no follow-up button on a Resolved ticket, and staff keep the status picker', async () => {
    await renderTicket(ticketWithStatus('st-resolved'))
    expect(screen.queryByRole('button', { name: 'Create follow-up' })).toBeNull()
    expect(screen.queryByRole('combobox', { name: 'Ticket status' })).not.toBeNull()
    expect(screen.queryByRole('note')).toBeNull()
  })

  it('still lets a requester reply', async () => {
    signInAs('user')
    await renderTicket(ticketWithStatus('st-resolved'))
    expect(screen.queryByPlaceholderText(/type your reply/i)).not.toBeNull()
  })
})
