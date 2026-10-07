import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #349: Closed is archived read-only, and terminal by default. A requester can
// read it and change nothing; staff and admin continue the work in a new,
// linked follow-up ticket, and get a Reopen button only when the instance's
// closed_reopen_policy lets their role (the server says so in can_reopen).

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

function ticketWithStatus(statusId: string): Record<string, unknown> & { id: string; status_id: string } {
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

// closed_reopen_policy (#349): the server says, per ticket and per viewer,
// whether the Reopen button is for them (can_reopen).
describe('force-reopening a closed ticket', () => {
  function closedTicket(canReopen?: boolean) {
    return { ...ticketWithStatus('st-closed'), ...(canReopen === undefined ? {} : { can_reopen: canReopen }) }
  }

  it('shows no Reopen button when the server does not say the viewer may (the default)', async () => {
    await renderTicket(closedTicket(false))
    expect(screen.queryByRole('button', { name: 'Reopen' })).toBeNull()
    expect(screen.getByRole('note').textContent).toMatch(/cannot be reopened/i)
  })

  it('shows it, the status picker and a different note when the viewer may', async () => {
    await renderTicket(closedTicket(true))
    expect(screen.queryByRole('button', { name: 'Reopen' })).not.toBeNull()
    expect(screen.queryByRole('button', { name: 'Create follow-up' })).not.toBeNull()
    expect(screen.queryByRole('combobox', { name: 'Ticket status' })).not.toBeNull()
    expect(screen.getByRole('note').textContent).toMatch(/You can reopen it/i)
  })

  it('posts to the reopen endpoint when clicked', async () => {
    await renderTicket(closedTicket(true))
    const post = vi.spyOn(api, 'post').mockResolvedValue({ data: closedTicket(true) } as never)

    await userEvent.click(screen.getByRole('button', { name: 'Reopen' }))

    await waitFor(() => expect(post).toHaveBeenCalledWith(`/tickets/${TICKET_ID}/reopen`, {}))
  })

  it('shows the server message, and refetches, when the policy changed under the page', async () => {
    const getSpy = await renderTicket(closedTicket(true))
    vi.spyOn(api, 'post').mockRejectedValue({
      isAxiosError: true,
      response: {
        data: { error: { code: 'ticket_closed', message: 'reopening closed tickets is disabled on this instance' } },
      },
    })
    const before = getSpy.mock.calls.filter((c) => c[0] === `/tickets/${TICKET_ID}`).length

    await userEvent.click(screen.getByRole('button', { name: 'Reopen' }))

    await waitFor(() => {
      expect(screen.getByRole('alert').textContent).toContain('reopening closed tickets is disabled')
    })
    await waitFor(() => {
      const after = getSpy.mock.calls.filter((c) => c[0] === `/tickets/${TICKET_ID}`).length
      expect(after).toBeGreaterThan(before)
    })
  })

  it('never shows it to a requester, even if the field were somehow true', async () => {
    signInAs('user')
    await renderTicket(closedTicket(true))
    expect(screen.queryByRole('button', { name: 'Reopen' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Create follow-up' })).toBeNull()
  })

  it('shows no Reopen button on a Resolved ticket whatever the field says', async () => {
    await renderTicket({ ...ticketWithStatus('st-resolved'), can_reopen: true })
    expect(screen.queryByRole('button', { name: 'Reopen' })).toBeNull()
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
