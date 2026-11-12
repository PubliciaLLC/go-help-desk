import { describe, expect, it, vi, beforeEach } from 'vitest'
import { waitFor } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import type { Ticket } from '@/api/types'

// #628: staff scanning the queue can tell which tickets hold an infected file
// without opening each one. The server sends `has_quarantined_attachment` to
// staff and administrators only, and only when it is true; the list renders a
// quiet badge beside Priority, next to Reopened.

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

function ticket(over: Partial<Ticket> & { id: string; subject: string }): Ticket {
  return {
    tracking_number: `TKT-${over.id}`,
    description: '',
    description_format: 'plain',
    category_id: 'cat-1',
    priority: 'medium',
    status_id: 'st-new',
    created_at: '2026-09-20T10:00:00Z',
    updated_at: '2026-09-20T10:00:00Z',
    ...over,
  }
}

const INFECTED = ticket({ id: 'tk-1', subject: 'Holds a sample', has_quarantined_attachment: true })
const CLEAN = ticket({ id: 'tk-2', subject: 'Nothing wrong' })

const BADGE = '[data-testid="quarantined-badge"]'

function mockApi(tickets: Ticket[]) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/tickets') return Promise.resolve({ data: tickets })
    if (url === '/statuses') return Promise.resolve({ data: STATUSES })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

function signInAs(role: 'staff' | 'user') {
  useAuthStore.setState({
    user: {
      id: 'u-1',
      email: 'someone@example.com',
      display_name: 'Someone',
      role,
      mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })
}

async function renderList(tickets: Ticket[]) {
  mockApi(tickets)
  renderWithQuery(<TicketListPage />)
  await waitFor(() => {
    expect(document.body.textContent).toContain(tickets[0].subject)
  })
}

/** The table row, or mobile card, that belongs to one ticket. */
function containersFor(subject: string): HTMLElement[] {
  return Array.from(document.querySelectorAll<HTMLElement>('tr, li')).filter((el) =>
    (el.textContent ?? '').includes(subject),
  )
}

describe('the quarantine marker in the ticket list', () => {
  beforeEach(() => {
    signInAs('staff')
  })

  it('marks the ticket that holds a quarantined file, in the table and the mobile card', async () => {
    await renderList([INFECTED, CLEAN])

    const marked = containersFor(INFECTED.subject)
    expect(marked.length, 'the ticket is rendered as a row and as a card').toBeGreaterThanOrEqual(2)
    for (const el of marked) {
      expect(el.querySelector(BADGE), 'a ticket with a quarantined file carries no marker').not.toBeNull()
    }
  })

  it('leaves every other ticket unmarked', async () => {
    await renderList([INFECTED, CLEAN])

    for (const el of containersFor(CLEAN.subject)) {
      expect(el.querySelector(BADGE), 'a ticket with nothing quarantined is marked').toBeNull()
    }
    expect(document.querySelectorAll(BADGE).length).toBe(2) // row + card of the one ticket
  })

  it('says what it means in words, not only a colour', async () => {
    await renderList([INFECTED, CLEAN])

    const badge = document.querySelector(BADGE)
    expect(badge?.textContent ?? '').toMatch(/quarantined/i)
    expect(badge?.getAttribute('title') ?? '').toMatch(/malicious/i)
  })

  it('is not shown to a requester, even if a response carried the key', async () => {
    signInAs('user')
    await renderList([INFECTED, CLEAN])

    expect(document.querySelectorAll(BADGE).length).toBe(0)
  })
})
