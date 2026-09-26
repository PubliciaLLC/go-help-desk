import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #277: Reopen is Closed-only on the server. Staff must not see the button on
// a Resolved ticket (it always 500'd there), and a click that still fails
// (someone else already reopened it) must show a message rather than a
// swallowed error.

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

describe('reopening a ticket', () => {
  it('shows no Reopen button on a Resolved ticket', async () => {
    mockApiGet(ticketWithStatus('st-resolved'))
    renderWithQuery(<TicketDetailPage />)

    await waitFor(() => {
      expect(document.body.textContent).toContain('Printer is on fire')
    })

    expect(screen.queryByRole('button', { name: 'Reopen' })).toBeNull()
  })

  it('shows the Reopen button on a Closed ticket', async () => {
    mockApiGet(ticketWithStatus('st-closed'))
    renderWithQuery(<TicketDetailPage />)

    await waitFor(() => {
      expect(document.body.textContent).toContain('Printer is on fire')
    })

    expect(screen.queryByRole('button', { name: 'Reopen' })).not.toBeNull()
  })

  it('shows the server message when reopen fails with 409 ticket_not_closed', async () => {
    mockApiGet(ticketWithStatus('st-closed'))
    vi.spyOn(api, 'post').mockRejectedValue({
      isAxiosError: true,
      response: {
        data: { error: { code: 'ticket_not_closed', message: 'only a closed ticket can be reopened' } },
      },
    })
    renderWithQuery(<TicketDetailPage />)

    await waitFor(() => {
      expect(document.body.textContent).toContain('Printer is on fire')
    })

    await userEvent.click(screen.getByRole('button', { name: 'Reopen' }))

    await waitFor(() => {
      const alert = screen.getByRole('alert')
      expect(alert.textContent).toContain('only a closed ticket can be reopened')
    })
  })
})
