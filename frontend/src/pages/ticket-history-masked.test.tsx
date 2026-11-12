import { describe, expect, it, vi, beforeEach } from 'vitest'
import { waitFor } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// The status timeline masks a requester's name like the Activity feed does
// (audit_mask_requester_names). The server sends the label and
// changed_by_masked; the page shows the same "Requester (name hidden)" the
// audit views do, and an account that happens to be called "Requester" is not
// mistaken for it.

const TICKET_ID = 'tkt-1'

vi.mock('@tanstack/react-router', () => ({
  useParams: () => ({ id: TICKET_ID }),
  useNavigate: () => vi.fn(),
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
  { id: 'st-prog', name: 'In Progress', kind: 'custom', sort_order: 2, color: '#fa0', active: true, ticket_count: 0 },
]

const TICKET = {
  id: TICKET_ID,
  tracking_number: 'TKT-0001',
  subject: 'Printer is on fire',
  description: 'Literally.',
  category_id: 'cat-1',
  priority: 'high',
  status_id: 'st-prog',
  created_at: '2026-09-20T10:00:00Z',
  updated_at: '2026-09-20T10:00:00Z',
}

const row = (id: string, name: string, masked: boolean) => ({
  id, ticket_id: TICKET_ID, from_status_id: 'st-new', from_status_name: 'New', from_status_color: '#888',
  to_status_id: 'st-prog', to_status_name: 'In Progress', to_status_color: '#fa0',
  changed_by_user_id: 'u-9', changed_by_name: name, changed_by_masked: masked || undefined,
  created_at: '2026-09-21T10:00:00Z',
})

function mockApi(history: unknown[]) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === `/tickets/${TICKET_ID}`) return Promise.resolve({ data: TICKET })
    if (url === `/tickets/${TICKET_ID}/history`) return Promise.resolve({ data: history })
    if (url === '/statuses') return Promise.resolve({ data: STATUSES })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

beforeEach(() => {
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'admin@example.com', display_name: 'Admin', role: 'admin', mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
})

describe('status timeline requester mask', () => {
  it('shows a masked requester as hidden, not by the label the server sent', async () => {
    mockApi([row('h1', 'Requester', true)])
    renderWithQuery(<TicketDetailPage />)
    await waitFor(() => expect(document.body.textContent).toMatch(/New\s*→\s*In Progress/))
    expect(document.body.textContent).toContain('Requester (name hidden)')
  })

  it('shows an account called "Requester" by name when it is not the mask', async () => {
    mockApi([row('h1', 'Requester', false)])
    renderWithQuery(<TicketDetailPage />)
    await waitFor(() => expect(document.body.textContent).toMatch(/New\s*→\s*In Progress/))
    expect(document.body.textContent).toContain('· Requester')
    expect(document.body.textContent).not.toContain('name hidden')
  })
})
