import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #443: "Resolve all children" lives in the Linked Tickets panel, which the
// ticket page mounts for staff and admin only. A reporting user never sees it.

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

function ticket(id: string) {
  return {
    id,
    tracking_number: `TKT-${id}`,
    subject: id === TICKET_ID ? 'Parent outage' : `Child ${id}`,
    description: '',
    category_id: 'cat-1',
    priority: 'high',
    status_id: 'st-new',
    created_at: '2026-09-20T10:00:00Z',
    updated_at: '2026-09-20T10:00:00Z',
    sla: null,
  }
}

function mockApiGet() {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === `/tickets/${TICKET_ID}`) return Promise.resolve({ data: ticket(TICKET_ID) })
    if (url === '/tickets/child-1') return Promise.resolve({ data: ticket('child-1') })
    if (url === `/tickets/${TICKET_ID}/links`) {
      return Promise.resolve({ data: [{ source_id: TICKET_ID, target_id: 'child-1', link_type: 'parent_child' }] })
    }
    if (url === '/statuses') {
      return Promise.resolve({ data: [{ id: 'st-new', name: 'New', kind: 'system', sort_order: 1, color: '#888', active: true, ticket_count: 1 }] })
    }
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
  vi.restoreAllMocks()
})

describe('Resolve all children on the ticket page', () => {
  it('is offered to staff and admin on a parent ticket', async () => {
    for (const role of ['staff', 'admin'] as const) {
      signInAs(role)
      mockApiGet()
      const { unmount } = renderWithQuery(<TicketDetailPage />)
      expect(await screen.findByRole('button', { name: 'Resolve all children' })).toBeDefined()
      unmount()
    }
  })

  it('is not offered to a reporting user', async () => {
    signInAs('user')
    mockApiGet()
    renderWithQuery(<TicketDetailPage />)

    await waitFor(() => expect(document.body.textContent).toContain('Parent outage'))
    expect(screen.queryByRole('button', { name: 'Resolve all children' })).toBeNull()
    expect(screen.queryByText('Linked Tickets')).toBeNull()
  })
})
