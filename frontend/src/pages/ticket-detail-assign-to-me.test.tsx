import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError } from 'axios'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #444: "Assign to me" in the Assignee panel. Unassigned tickets only, staff
// and admin only, never on a Resolved or Closed ticket. The ticket's group is
// KEPT: the server's Assign sets both assignee fields from what the request
// carries, so a user-only PATCH would clear the group. The button therefore
// sends the ticket's own group back with the user.

const TICKET_ID = 'tkt-1'
const ME = 'u-1'

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

function mockApiGet(extra: Record<string, unknown> = {}) {
  const ticket = {
    id: TICKET_ID,
    tracking_number: 'TKT-0001',
    subject: 'Printer is on fire',
    description: 'Literally.',
    category_id: 'cat-1',
    priority: 'high',
    status_id: 'st-new',
    created_at: '2026-09-20T10:00:00Z',
    updated_at: '2026-09-20T10:00:00Z',
    sla: null,
    ...extra,
  }
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === `/tickets/${TICKET_ID}`) return Promise.resolve({ data: ticket })
    if (url === '/statuses') return Promise.resolve({ data: STATUSES })
    if (url === '/staff') {
      return Promise.resolve({
        data: [
          { id: ME, display_name: 'Sam', assignable: true },
          { id: 'u-2', display_name: 'Riley', assignable: true },
        ],
      })
    }
    if (url === '/groups') return Promise.resolve({ data: [{ id: 'g-1', name: 'Network' }] })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

function signInAs(role: 'staff' | 'admin' | 'user') {
  useAuthStore.setState({
    user: {
      id: ME,
      email: `${role}@example.com`,
      display_name: 'Sam',
      role,
      mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })
}

async function renderTicket(extra: Record<string, unknown> = {}) {
  mockApiGet(extra)
  renderWithQuery(<TicketDetailPage />)
  await waitFor(() => expect(document.body.textContent).toContain('Printer is on fire'))
  // The statuses decide whether the ticket is closed; wait for them.
  await waitFor(() => expect(screen.queryByText('…')).toBeNull())
}

const button = () => screen.queryByRole('button', { name: 'Assign to me' })

beforeEach(() => {
  vi.restoreAllMocks()
  signInAs('staff')
})

describe('Assign to me: when it is offered', () => {
  it('is offered to staff and admin on an unassigned ticket', async () => {
    for (const role of ['staff', 'admin'] as const) {
      signInAs(role)
      mockApiGet()
      const { unmount } = renderWithQuery(<TicketDetailPage />)
      expect(await screen.findByRole('button', { name: 'Assign to me' })).toBeDefined()
      unmount()
    }
  })

  it('is hidden when the ticket is already assigned to me', async () => {
    await renderTicket({ assignee_user_id: ME })
    expect(button()).toBeNull()
  })

  it('is hidden when the ticket is assigned to somebody else', async () => {
    await renderTicket({ assignee_user_id: 'u-2' })
    expect(button()).toBeNull()
  })

  it('is hidden for a reporting user', async () => {
    signInAs('user')
    await renderTicket()
    expect(button()).toBeNull()
  })

  it('is hidden on a Resolved or Closed ticket', async () => {
    for (const statusId of ['st-resolved', 'st-closed']) {
      mockApiGet({ status_id: statusId })
      const { unmount } = renderWithQuery(<TicketDetailPage />)
      await waitFor(() => expect(document.body.textContent).toContain('Printer is on fire'))
      await waitFor(() => expect(screen.queryByText('…')).toBeNull())
      expect(button()).toBeNull()
      unmount()
    }
  })
})

describe('Assign to me: what it sends', () => {
  it('sends only the user when the ticket has no group', async () => {
    await renderTicket()
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: {} })

    await userEvent.click(screen.getByRole('button', { name: 'Assign to me' }))

    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patch).toHaveBeenCalledWith(`/tickets/${TICKET_ID}`, { assignee_user_id: ME })
  })

  // The reason the group key exists at all: the server's Assign clears the
  // group when only the user is sent (PATCH → ticket.Service.Assign sets both
  // fields from its arguments). Erik's decision is that the group is kept, so
  // the ticket's own group is sent back with the user.
  it('sends the ticket\'s own group with the user so the server keeps it', async () => {
    await renderTicket({ assignee_group_id: 'g-1' })
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: {} })

    await userEvent.click(screen.getByRole('button', { name: 'Assign to me' }))

    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patch).toHaveBeenCalledWith(`/tickets/${TICKET_ID}`, { assignee_user_id: ME, assignee_group_id: 'g-1' })
  })

  it('shows the server\'s message when it is refused', async () => {
    await renderTicket()
    vi.spyOn(api, 'patch').mockRejectedValue(
      new AxiosError('failed', '400', undefined, undefined, {
        status: 400,
        data: { error: { code: 'bad_request', message: 'that account cannot be given a ticket' } },
        statusText: '',
        headers: {},
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        config: {} as any,
      }),
    )

    await userEvent.click(screen.getByRole('button', { name: 'Assign to me' }))

    expect((await screen.findByRole('alert')).textContent).toBe('that account cannot be given a ticket')
  })
})
