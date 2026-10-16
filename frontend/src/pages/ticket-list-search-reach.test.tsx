import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #444: a staff or admin search covers every ticket the viewer can see
// (scope=visible), whatever queue is selected, and the "Only <queue>" chip
// narrows it back to the queue.

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
  get = vi.spyOn(api, 'get').mockImplementation((() => Promise.resolve({ data: [] })) as never)
})

async function typeSearch(text: string) {
  fireEvent.change(screen.getByPlaceholderText(/Search by tracking number/), { target: { value: text } })
}

describe('search reach for staff', () => {
  it('an empty box asks for the queue, exactly as before, and shows no label or chip', async () => {
    setRole('staff')
    renderWithQuery(<TicketListPage />)
    await waitFor(() => expect(ticketCalls().length).toBeGreaterThan(0))

    expect(ticketCalls().every(p => p.scope === 'mine' && p.q === undefined)).toBe(true)
    expect(screen.queryByText('Searching all tickets you can see')).toBeNull()
    expect(screen.queryByRole('button', { name: /^Only / })).toBeNull()
  })

  it('typing searches everything visible and says so', async () => {
    setRole('staff')
    renderWithQuery(<TicketListPage />)
    await typeSearch('printer')

    await screen.findByText('Searching all tickets you can see')
    await waitFor(() => expect(ticketCalls().at(-1)).toMatchObject({ q: 'printer', scope: 'visible' }))
  })

  it('the chip narrows to the queue, and clicking it again widens it', async () => {
    setRole('staff')
    renderWithQuery(<TicketListPage />)
    await typeSearch('printer')

    const chip = await screen.findByRole('button', { name: 'Only my tickets' })
    expect(chip.getAttribute('aria-pressed')).toBe('false')

    fireEvent.click(chip)
    await waitFor(() => expect(ticketCalls().at(-1)).toMatchObject({ q: 'printer', scope: 'mine' }))
    expect(screen.queryByText('Searching all tickets you can see')).toBeNull()
    expect(screen.getByRole('button', { name: /Only my tickets/ }).getAttribute('aria-pressed')).toBe('true')

    fireEvent.click(screen.getByRole('button', { name: /Only my tickets/ }))
    await waitFor(() => expect(ticketCalls().at(-1)).toMatchObject({ q: 'printer', scope: 'visible' }))
    await screen.findByText('Searching all tickets you can see')
  })
})

describe('search reach for requesters', () => {
  it('is unchanged: no scope=visible, no label, no chip', async () => {
    setRole('user')
    renderWithQuery(<TicketListPage />)
    await typeSearch('printer')

    await waitFor(() => expect(ticketCalls().at(-1)).toMatchObject({ q: 'printer' }))
    expect(ticketCalls().some(p => p.scope === 'visible')).toBe(false)
    expect(screen.queryByText('Searching all tickets you can see')).toBeNull()
    expect(screen.queryByRole('button', { name: /^Only / })).toBeNull()
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
    expect(screen.queryByRole('button', { name: /^Only / })).toBeNull()
  })
})

describe('the chip resets so every new search starts wide', () => {
  async function chipOn() {
    setRole('admin')
    renderWithQuery(<TicketListPage />)
    await typeSearch('printer')
    fireEvent.click(await screen.findByRole('button', { name: 'Only my tickets' }))
    await screen.findByRole('button', { name: /Only my tickets/ })
  }

  it('clearing the box turns it off', async () => {
    await chipOn()
    await typeSearch('')
    await typeSearch('toner')
    await waitFor(() => expect(ticketCalls().at(-1)).toMatchObject({ q: 'toner', scope: 'visible' }))
  })

  it('changing the queue turns it off', async () => {
    await chipOn()
    fireEvent.click(screen.getByRole('button', { name: 'unassigned' }))
    await waitFor(() => expect(ticketCalls().at(-1)).toMatchObject({ q: 'printer', scope: 'visible' }))
    expect(screen.getByRole('button', { name: 'Only unassigned' }).getAttribute('aria-pressed')).toBe('false')
  })

  it('refining the same search keeps it on', async () => {
    await chipOn()
    await typeSearch('printer jam')
    await waitFor(() => expect(ticketCalls().at(-1)).toMatchObject({ q: 'printer jam', scope: 'mine' }))
  })
})

describe('admin All queue', () => {
  it('shows the label, with no chip', async () => {
    setRole('admin')
    renderWithQuery(<TicketListPage />)
    fireEvent.click(screen.getByRole('button', { name: 'all' }))
    await typeSearch('printer')

    await screen.findByText('Searching all tickets you can see')
    expect(screen.queryByRole('button', { name: /^Only / })).toBeNull()
  })
})

describe('the label', () => {
  it('is a polite live region', async () => {
    setRole('staff')
    renderWithQuery(<TicketListPage />)
    await typeSearch('printer')

    const label = await screen.findByText('Searching all tickets you can see')
    expect(label.getAttribute('aria-live')).toBe('polite')
  })
})
