import { describe, expect, it, vi, beforeEach } from 'vitest'
import { waitFor } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import type { Ticket, TicketSLA } from '@/api/types'

// The SLA column in the ticket queue (#183, docs/DESIGN.md → SLA Tracking →
// SLA Indicators): a compact indicator after Priority, shown only when the
// page actually has something to show, and absent entirely otherwise — same
// reasoning as the hash link in AttachmentList, applied to a whole column
// instead of a row.

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
  useSearch: () => ({ status: undefined, reporter: undefined }),
  useRouterState: () => ({ location: { pathname: '/tickets' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}))

import { TicketListPage } from './TicketListPage'

const STATUSES = [
  { id: 'st-new', name: 'New', kind: 'system', sort_order: 1, color: '#888', active: true, ticket_count: 1 },
]

function target(color: TicketSLA['response']['color']) {
  return { color, target_min: 60, elapsed_min: 12, remaining_min: 48, met_at: null }
}

function ticket(over: Partial<Ticket> & { id: string; subject: string }): Ticket {
  return {
    tracking_number: `TKT-${over.id}`,
    description: '',
    category_id: 'cat-1',
    priority: 'medium',
    status_id: 'st-new',
    created_at: '2026-09-20T10:00:00Z',
    updated_at: '2026-09-20T10:00:00Z',
    ...over,
  }
}

const GREEN_AMBER = ticket({
  id: 'tk-1',
  subject: 'Green response, amber resolution',
  sla: {
    policy_id: 'pol-1',
    policy_name: 'Standard',
    response: target('green'),
    resolution: target('amber'),
  },
})

const RED_RED = ticket({
  id: 'tk-2',
  subject: 'Both red',
  sla: {
    policy_id: 'pol-1',
    policy_name: 'Standard',
    response: target('red'),
    resolution: target('red'),
  },
})

const NO_POLICY = ticket({ id: 'tk-3', subject: 'No matching policy', sla: null })

function mockApi(tickets: Ticket[]) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/tickets') return Promise.resolve({ data: tickets })
    if (url === '/statuses') return Promise.resolve({ data: STATUSES })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

beforeEach(() => {
  // Staff, not admin: an admin also fires listUsers and security-warnings,
  // neither of which this suite is about.
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

describe('the SLA column', () => {
  it('appears with a header, and rows with a status carry a coloured indicator', async () => {
    mockApi([GREEN_AMBER, RED_RED, NO_POLICY])
    renderWithQuery(<TicketListPage />)

    await waitFor(() => {
      expect(document.body.textContent).toContain(GREEN_AMBER.subject)
    })

    const header = Array.from(document.querySelectorAll('th')).find((th) => th.textContent === 'SLA')
    expect(header, 'no SLA column header once at least one row has a status').not.toBeUndefined()

    const rows = Array.from(document.querySelectorAll('tbody tr'))
    expect(rows).toHaveLength(3)

    const row1 = rows.find((r) => r.textContent?.includes(GREEN_AMBER.subject))!
    const row2 = rows.find((r) => r.textContent?.includes(RED_RED.subject))!
    const row3 = rows.find((r) => r.textContent?.includes(NO_POLICY.subject))!

    expect(row1.querySelectorAll('[data-target]').length).toBe(2)
    expect(row1.querySelector('[data-target="response"]')?.getAttribute('data-color')).toBe('green')
    expect(row1.querySelector('[data-target="resolution"]')?.getAttribute('data-color')).toBe('amber')

    expect(row2.querySelector('[data-target="response"]')?.getAttribute('data-color')).toBe('red')
    expect(row2.querySelector('[data-target="resolution"]')?.getAttribute('data-color')).toBe('red')

    // A ticket with no matching policy has no indicator at all, not green.
    expect(row3.querySelectorAll('[data-target]').length).toBe(0)
  })

  it('is absent entirely when every ticket on the page has sla: null', async () => {
    mockApi([{ ...NO_POLICY, id: 'tk-4' }, { ...NO_POLICY, id: 'tk-5', subject: 'Also no policy' }])
    renderWithQuery(<TicketListPage />)

    await waitFor(() => {
      expect(document.body.textContent).toContain('Also no policy')
    })

    const header = Array.from(document.querySelectorAll('th')).find((th) => th.textContent === 'SLA')
    expect(header, 'an SLA column appeared with nothing on the page to show in it').toBeUndefined()
    expect(document.querySelectorAll('[data-target]').length).toBe(0)
  })
})
