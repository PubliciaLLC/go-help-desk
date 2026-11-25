import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #444, reshaped by #676: the Search view covers every ticket the viewer can
// see (scope=visible); on a queue view text and filters narrow within the queue,
// and the "Only <queue>" chip that widened and narrowed it is gone.

const search = vi.hoisted(() => ({ value: {} as Record<string, unknown> }))

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
  useSearch: () => search.value,
  useRouterState: () => ({ location: { pathname: '/tickets' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}))

import { TicketListPage } from './TicketListPage'

function setRole(role: 'staff' | 'user' | 'admin') {
  useAuthStore.setState({
    user: {
      id: 'u-1',
      email: 'x@example.com',
      display_name: 'Pat',
      role,
      mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })
}

let get: ReturnType<typeof vi.spyOn>

function ticketCalls(): Record<string, unknown>[] {
  return get.mock.calls
    .filter((c: unknown[]) => c[0] === '/tickets')
    .map((c: unknown[]) => (c[1] as { params: Record<string, unknown> }).params)
}

beforeEach(() => {
  search.value = {}
  get = vi.spyOn(api, 'get').mockImplementation((() => Promise.resolve({ data: [] })) as never)
})

async function typeSearch(text: string) {
  fireEvent.change(screen.getByPlaceholderText(/Search by tracking number/), { target: { value: text } })
}

describe('search reach for staff', () => {
  it('an empty box on the default view asks for the tickets assigned to them, and shows no label or chip', async () => {
    setRole('staff')
    renderWithQuery(<TicketListPage />)
    await waitFor(() => expect(ticketCalls().length).toBeGreaterThan(0))

    expect(ticketCalls().every(p => p.scope === 'assigned_to_me' && p.q === undefined)).toBe(true)
    expect(screen.queryByText('Searching all tickets you can see')).toBeNull()
    expect(screen.queryByRole('button', { name: /^Only/ })).toBeNull()
  })

  it.each(['me', 'groups'] as const)('typing on the %s view stays in the queue', async view => {
    setRole('staff')
    search.value = { view }
    renderWithQuery(<TicketListPage />)
    await typeSearch('printer')

    await waitFor(() =>
      expect(ticketCalls().at(-1)).toMatchObject({ q: 'printer', scope: view === 'me' ? 'assigned_to_me' : 'my_groups' }),
    )
    expect(ticketCalls().some(p => p.scope === 'visible')).toBe(false)
    expect(screen.queryByText('Searching all tickets you can see')).toBeNull()
    expect(screen.queryByRole('button', { name: /^Only/ })).toBeNull()
  })

  it('the Search view covers everything visible and says so, with an empty box too', async () => {
    setRole('staff')
    search.value = { view: 'search' }
    renderWithQuery(<TicketListPage />)
    await screen.findByText('Searching all tickets you can see')
    await waitFor(() => expect(ticketCalls().at(-1)).toMatchObject({ scope: 'visible' }))

    await typeSearch('printer')
    await waitFor(() => expect(ticketCalls().at(-1)).toMatchObject({ q: 'printer', scope: 'visible' }))
    expect(screen.queryByRole('button', { name: /^Only/ })).toBeNull()
  })
})

describe('search reach for requesters', () => {
  it('is unchanged: no scope=visible, no label, no chip', async () => {
    setRole('user')
    renderWithQuery(<TicketListPage />)
    await typeSearch('printer')

    await waitFor(() => expect(ticketCalls().at(-1)).toMatchObject({ q: 'printer', scope: 'mine' }))
    expect(ticketCalls().some(p => p.scope === 'visible')).toBe(false)
    expect(screen.queryByText('Searching all tickets you can see')).toBeNull()
    expect(screen.queryByRole('button', { name: /^Only/ })).toBeNull()
  })
})

describe('whitespace', () => {
  it('a box holding only spaces behaves like an empty box', async () => {
    setRole('staff')
    renderWithQuery(<TicketListPage />)
    await typeSearch('   ')
    // Past the 300 ms debounce.
    await new Promise(r => setTimeout(r, 450))

    expect(ticketCalls().some(p => p.scope === 'visible')).toBe(false)
    expect(screen.queryByText('Searching all tickets you can see')).toBeNull()
  })
})

// `main`: the sidebar's appearance switch (#709) has a live region of its own, which comes first in the document.
describe('the label', () => {
  it('is a polite live region that is in the page before it has anything to say', () => {
    setRole('staff')
    search.value = { view: 'me' }
    const { container } = renderWithQuery(<TicketListPage />)
    const region = container.querySelector('main [aria-live="polite"]')
    expect(region).not.toBeNull()
    expect(region!.textContent).toBe('')
  })

  it('says what Search covers in the same region', async () => {
    setRole('staff')
    search.value = { view: 'search' }
    const { container } = renderWithQuery(<TicketListPage />)
    const region = container.querySelector('main [aria-live="polite"]')
    const label = await screen.findByText('Searching all tickets you can see')
    expect(label).toBe(region)
  })
})
