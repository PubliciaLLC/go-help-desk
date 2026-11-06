import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #441: the followers panel is on the ticket page for staff and admin, and a
// requester neither sees it nor causes a request the server would refuse.

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

function signInAs(role: 'staff' | 'admin' | 'user') {
  useAuthStore.setState({
    user: {
      id: 'u-1', email: `${role}@example.com`, display_name: 'Sam', role, mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
}

function serve() {
  return vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === `/tickets/${TICKET_ID}`) {
      return Promise.resolve({
        data: {
          id: TICKET_ID, tracking_number: 'TKT-0001', subject: 'Printer is on fire', description: 'x',
          category_id: 'cat-1', priority: 'low', status_id: 'st-new',
          created_at: '2026-09-20T10:00:00Z', updated_at: '2026-09-20T10:00:00Z', sla: null,
        },
      })
    }
    if (url === `/tickets/${TICKET_ID}/followers`) {
      return Promise.resolve({ data: [{ user_id: 'u-9', display_name: 'Alex Admin', created_at: '2026-10-01T09:00:00Z' }] })
    }
    if (url === `/tickets/${TICKET_ID}/audit`) {
      return Promise.resolve({
        data: [
          { id: 'a1', action: 'followed', actor_id: 'u-9', actor_name: 'Alex Admin', created_at: '2026-10-01T09:00:00Z' },
          { id: 'a2', action: 'unfollowed', actor_id: 'u-9', actor_name: 'Alex Admin', created_at: '2026-10-01T10:00:00Z' },
        ],
      })
    }
    if (url === '/statuses') {
      return Promise.resolve({ data: [{ id: 'st-new', name: 'New', kind: 'system', sort_order: 1, color: '#888', active: true, ticket_count: 1 }] })
    }
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

beforeEach(() => signInAs('staff'))
afterEach(() => vi.restoreAllMocks())

describe('the ticket page and followers', () => {
  it.each(['staff', 'admin'] as const)('shows %s who follows, and a Follow button', async (role) => {
    signInAs(role)
    serve()
    renderWithQuery(<TicketDetailPage />)

    expect(await screen.findByRole('button', { name: 'Follow' })).toBeTruthy()
    // In the followers list, as well as the activity feed.
    expect((await screen.findAllByText('Alex Admin')).length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText('Followers')).toBeTruthy()
  })

  it('names follow and unfollow in the activity feed', async () => {
    serve()
    renderWithQuery(<TicketDetailPage />)

    expect(await screen.findByText('Started following')).toBeTruthy()
    expect(screen.getByText('Stopped following')).toBeTruthy()
  })

  it('shows a requester no followers panel and never asks for the list', async () => {
    signInAs('user')
    const get = serve()
    renderWithQuery(<TicketDetailPage />)
    await waitFor(() => expect(document.body.textContent).toContain('Printer is on fire'))

    expect(screen.queryByText('Followers')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Follow' })).toBeNull()
    expect(get.mock.calls.map((c) => c[0])).not.toContain(`/tickets/${TICKET_ID}/followers`)
  })
})
