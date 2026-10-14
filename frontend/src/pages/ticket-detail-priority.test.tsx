import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #443: the priority control in the ticket page's Details card. Staff and
// admin get a select; a reporting user keeps the plain badge (the API answers
// 403 to them); a Resolved or Closed ticket shows it disabled, because the
// server answers 409 ticket_closed there.

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

function mockApiGet(statusId: string) {
  const ticket = {
    id: TICKET_ID,
    tracking_number: 'TKT-0001',
    subject: 'Printer is on fire',
    description: 'Literally.',
    category_id: 'cat-1',
    priority: 'high',
    status_id: statusId,
    created_at: '2026-09-20T10:00:00Z',
    updated_at: '2026-09-20T10:00:00Z',
    sla: null,
  }
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
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

async function renderTicket(statusId: string) {
  mockApiGet(statusId)
  renderWithQuery(<TicketDetailPage />)
  await waitFor(() => {
    expect(document.body.textContent).toContain('Printer is on fire')
  })
}

beforeEach(() => {
  vi.restoreAllMocks()
  signInAs('staff')
})

describe('the priority control on the ticket page', () => {
  it('lets staff and admin change it, sending PATCH {priority}', async () => {
    for (const role of ['staff', 'admin'] as const) {
      signInAs(role)
      mockApiGet('st-new')
      const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: {} })
      const { unmount } = renderWithQuery(<TicketDetailPage />)

      const select = (await screen.findByLabelText('Ticket priority')) as HTMLSelectElement
      expect(select.value).toBe('high')
      await userEvent.selectOptions(select, 'critical')

      await waitFor(() => expect(patch).toHaveBeenCalledWith(`/tickets/${TICKET_ID}`, { priority: 'critical' }))
      unmount()
    }
  })

  it('is not rendered for a reporting user, who keeps the badge', async () => {
    signInAs('user')
    await renderTicket('st-new')

    expect(screen.queryByLabelText('Ticket priority')).toBeNull()
    expect(screen.getAllByText('high').length).toBeGreaterThan(0)
  })

  it('is disabled, with the reason, on a Resolved or Closed ticket', async () => {
    for (const statusId of ['st-resolved', 'st-closed']) {
      mockApiGet(statusId)
      const { unmount } = renderWithQuery(<TicketDetailPage />)

      const select = (await screen.findByLabelText('Ticket priority')) as HTMLSelectElement
      expect(select.disabled).toBe(true)
      expect(screen.getByText(/cannot be changed on a resolved or closed ticket/i)).toBeDefined()
      unmount()
    }
  })
})
