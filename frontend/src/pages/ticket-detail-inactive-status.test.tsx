import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #419: an inactive status is retired. The server refuses a move into it for
// every role, so the picker must not offer it. A ticket already sitting in one
// still has to show it, or the select would display the wrong status.

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
  { id: 'st-wait', name: 'Waiting on vendor', kind: 'custom', sort_order: 2, color: '#09f', active: true, ticket_count: 0 },
  { id: 'st-old', name: 'Retired thing', kind: 'custom', sort_order: 3, color: '#f90', active: false, ticket_count: 1 },
  { id: 'st-resolved', name: 'Resolved', kind: 'system', sort_order: 4, color: '#4caf50', active: true, ticket_count: 0 },
]

function mockApiGet(statusId: string) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === `/tickets/${TICKET_ID}`) {
      return Promise.resolve({
        data: {
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
        },
      })
    }
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
      display_name: 'Sam',
      role: 'staff',
      mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })
})

async function picker(statusId: string) {
  mockApiGet(statusId)
  renderWithQuery(<TicketDetailPage />)
  await waitFor(() => expect(document.body.textContent).toContain('Printer is on fire'))
  return screen.findByRole('combobox', { name: 'Ticket status' })
}

describe('the ticket status picker', () => {
  it('does not offer an inactive status', async () => {
    const select = await picker('st-new')
    await waitFor(() => expect(within(select).queryAllByRole('option').length).toBeGreaterThan(1))
    const names = within(select).getAllByRole('option').map((o) => o.textContent)
    expect(names).toEqual(['New', 'Waiting on vendor', 'Resolved'])
  })

  it('still shows the inactive status a ticket is already in, so the select is truthful', async () => {
    const select = await picker('st-old')
    await waitFor(() => expect(within(select).queryAllByRole('option').length).toBeGreaterThan(1))
    const names = within(select).getAllByRole('option').map((o) => o.textContent)
    expect(names).toContain('Retired thing')
    expect((select as HTMLSelectElement).value).toBe('st-old')
  })
})
