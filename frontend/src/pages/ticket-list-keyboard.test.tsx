import { describe, expect, it, vi, beforeEach } from 'vitest'
import { waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import type { Ticket } from '@/api/types'

// Opening a ticket from the queue, by keyboard.
//
// Each row was a `<tr onClick={navigate}>` with no link in any cell, no
// tabIndex and no key handler. Tabbing went search box → checkboxes (staff
// only) → Previous/Next and never landed on a ticket. For a reporting user
// there was no way in at all: "Jump to ticket" is staff-only, so the queue
// was a list they could read and not open.
//
// The fix is an anchor, not a key handler on the row — an anchor is also
// what gives middle-click, open-in-new-tab and copy-link, none of which a
// keydown listener provides.

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
  useSearch: () => ({ status: undefined, reporter: undefined }),
  useRouterState: () => ({ location: { pathname: '/tickets' } }),
  // The real Link renders an anchor whose href comes from `to` with the
  // params substituted. Mirrored here so the test asserts the href a reader
  // would actually get.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, params, children, ...rest }: any) => (
    <a href={params ? String(to).replace('$id', params.id) : to} {...rest}>
      {children}
    </a>
  ),
}))

import { TicketListPage } from './TicketListPage'

const STATUSES = [
  { id: 'st-new', name: 'New', kind: 'system', sort_order: 1, color: '#888', active: true, ticket_count: 1 },
]

function ticket(id: string, subject: string): Ticket {
  return {
    id,
    subject,
    tracking_number: `GHD-2026-00000${id.slice(-1)}`,
    description: '',
    category_id: 'cat-1',
    priority: 'medium',
    status_id: 'st-new',
    created_at: '2026-09-20T10:00:00Z',
    updated_at: '2026-09-20T10:00:00Z',
  } as Ticket
}

const TICKETS = [ticket('tk-1', 'Printer on fire'), ticket('tk-2', 'VPN drops hourly')]

function mockApi(tickets: Ticket[] = TICKETS) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/tickets') return Promise.resolve({ data: tickets })
    if (url === '/statuses') return Promise.resolve({ data: STATUSES })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

function signIn(role: 'user' | 'staff') {
  useAuthStore.setState({
    user: {
      id: 'u-1',
      email: `${role}@example.com`,
      display_name: role === 'user' ? 'Rita Reporter' : 'Sam Staff',
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

describe('opening a ticket from the queue', () => {
  // The role that had no other route in.
  it('gives a reporting user a link to every ticket on the page', async () => {
    signIn('user')
    mockApi()
    renderWithQuery(<TicketListPage />)

    await waitFor(() => {
      expect(document.body.textContent).toContain('Printer on fire')
    })

    for (const t of TICKETS) {
      const link = Array.from(document.querySelectorAll('tbody a')).find(
        (a) => a.textContent?.trim() === t.subject,
      )
      expect(link, `no link to "${t.subject}" — a keyboard user cannot open it`).not.toBeUndefined()
      expect(link!.getAttribute('href')).toBe(`/tickets/${t.id}`)
    }
  })

  it('puts those links in the tab order, so tabbing reaches a ticket', async () => {
    signIn('user')
    mockApi()
    renderWithQuery(<TicketListPage />)

    await waitFor(() => {
      expect(document.body.textContent).toContain('Printer on fire')
    })

    // Tab until a ticket link takes focus. Bounded so a regression fails
    // rather than hangs.
    const user = userEvent.setup()
    let reached: Element | null = null
    for (let i = 0; i < 40 && !reached; i++) {
      await user.tab()
      const el = document.activeElement
      if (el && el.tagName === 'A' && el.getAttribute('href')?.startsWith('/tickets/tk-')) {
        reached = el
      }
    }
    expect(reached, 'tabbing through the page never reached a ticket').not.toBeNull()
  })

  // The selection controls are the other half of the row for staff, and a
  // checkbox with no label is unusable by anything that reads the page aloud.
  it('labels the staff selection checkboxes', async () => {
    signIn('staff')
    mockApi()
    renderWithQuery(<TicketListPage />)

    await waitFor(() => {
      expect(document.body.textContent).toContain('Printer on fire')
    })

    // Inside the table only. The "Include closed" toggle in the toolbar is
    // wrapped in a <label> with its own text, which is a better accessible
    // name than aria-label and would fail an aria-label check for the wrong
    // reason — the first version of this test did exactly that.
    const boxes = Array.from(document.querySelectorAll('table input[type="checkbox"]'))
    expect(boxes.length, 'no selection checkboxes rendered for staff').toBeGreaterThan(0)
    for (const b of boxes) {
      const label = b.getAttribute('aria-label') ?? ''
      expect(label, 'a selection checkbox has no accessible name').not.toBe('')
    }
    // The select-all and one per row.
    expect(boxes.length).toBe(TICKETS.length + 1)

    const toggle = Array.from(document.querySelectorAll('label')).find((l) =>
      l.textContent?.includes('Include closed'),
    )
    expect(toggle?.querySelector('input[type="checkbox"]'),
      'the "Include closed" toggle is not inside its label, so it has no name').not.toBeNull()
  })
})
