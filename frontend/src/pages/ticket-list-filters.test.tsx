import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import type { Ticket } from '@/api/types'

// The ticket list's filters, sort and reply matches, as the page wires them.
// The address is real here: navigate() writes it and useSearch() reads it back,
// which the other ticket-list suites (a fixed search, a navigate that does
// nothing) deliberately do not.

const router = vi.hoisted(() => ({
  search: {} as Record<string, unknown>,
  listeners: new Set<() => void>(),
  replaced: [] as boolean[],
}))

vi.mock('@tanstack/react-router', async () => {
  const React = await import('react')
  return {
    useSearch: () =>
      React.useSyncExternalStore(
        cb => {
          router.listeners.add(cb)
          return () => router.listeners.delete(cb)
        },
        () => router.search,
      ),
    useNavigate: () => {
      const nav = React.useRef(
        (opts: { search: Record<string, unknown> | ((p: Record<string, unknown>) => Record<string, unknown>); replace?: boolean }) => {
          router.search = typeof opts.search === 'function' ? opts.search(router.search) : opts.search
          router.replaced.push(Boolean(opts.replace))
          router.listeners.forEach(l => l())
        },
      )
      return nav.current
    },
    useRouterState: () => ({ location: { pathname: '/tickets' } }),
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    Link: ({ to, params, children, ...rest }: any) => (
      <a href={params ? String(to).replace('$id', params.id) : to} {...rest}>
        {children}
      </a>
    ),
  }
})

import { TicketListPage } from './TicketListPage'

const STATUSES = [
  { id: '00000000-0000-4000-8000-0000000000a1', name: 'New', kind: 'system', sort_order: 1, color: '#3b82f6', active: true, ticket_count: 1 },
  { id: '00000000-0000-4000-8000-0000000000a2', name: 'Closed', kind: 'system', sort_order: 2, color: '#6b7280', active: true, ticket_count: 0 },
]
const DANA = { id: '00000000-0000-4000-8000-0000000000b1', display_name: 'Dana Whitfield', email: 'dana@example.com' }

function ticket(over: Partial<Ticket> & { id: string; subject: string }): Ticket {
  return {
    tracking_number: `TKT-${over.id}`,
    description: '',
    description_format: 'plain',
    category_id: '00000000-0000-4000-8000-0000000000c1',
    priority: 'medium',
    status_id: '00000000-0000-4000-8000-0000000000a1',
    created_at: '2026-10-20T10:00:00Z',
    updated_at: '2026-10-21T10:00:00Z',
    ...over,
  }
}

const sla = {
  policy_id: 'p',
  policy_name: 'Standard',
  response: { color: 'green', target_min: 60, elapsed_min: 1, remaining_min: 59, met_at: null },
  resolution: { color: 'green', target_min: 60, elapsed_min: 1, remaining_min: 59, met_at: null },
} as const

let tickets: Ticket[]
let headers: Record<string, string>
let get: ReturnType<typeof vi.spyOn>

function ticketCalls(): Record<string, unknown>[] {
  return get.mock.calls
    .filter((c: unknown[]) => c[0] === '/tickets')
    .map((c: unknown[]) => (c[1] as { params: Record<string, unknown> }).params)
}
const lastCall = () => ticketCalls().at(-1)!

// `sla_enabled` is what GET /me says: absent for a requester and when the
// server could not read the setting, so a test that says nothing gets absent.
function setRole(role: 'staff' | 'user' | 'admin', sla_enabled?: boolean) {
  useAuthStore.setState({
    user: {
      ...(sla_enabled === undefined ? {} : { sla_enabled }),
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

// The page asks the browser whether it is at least md wide. jsdom's polyfill
// always says no, so a test that wants the desktop layout says yes.
function viewport(wide: boolean) {
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: wide && query.includes('min-width: 768px'),
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }))
}

function go(search: Record<string, unknown>) {
  router.search = search
}

beforeEach(() => {
  viewport(true)
  router.search = {}
  router.listeners.clear()
  router.replaced = []
  tickets = [ticket({ id: 'tk-1', subject: 'Printer jam' })]
  headers = {}
  get = vi.spyOn(api, 'get').mockImplementation(((url: string, cfg?: { params?: Record<string, unknown> }) => {
    if (url === '/tickets') return Promise.resolve({ data: tickets, headers })
    if (url === '/statuses') return Promise.resolve({ data: STATUSES })
    if (url === '/categories') return Promise.resolve({ data: [{ id: '00000000-0000-4000-8000-0000000000c1', name: 'Hardware' }, { id: '00000000-0000-4000-8000-0000000000c2', name: 'Software' }] })
    if (url === '/categories/00000000-0000-4000-8000-0000000000c1/types') return Promise.resolve({ data: [{ id: '00000000-0000-4000-8000-0000000000d1', name: 'Printers' }] })
    if (url === '/staff') return Promise.resolve({ data: [{ id: '00000000-0000-4000-8000-0000000000b2', display_name: 'Sam Staff', assignable: true }] })
    if (url === '/groups') return Promise.resolve({ data: [{ id: '00000000-0000-4000-8000-0000000000e1', name: 'Network' }] })
    if (url === '/users/lookup') {
      // ?id= answers one object; ?q= answers a list.
      if (cfg?.params?.id) return Promise.resolve({ data: DANA })
      return Promise.resolve({ data: [DANA] })
    }
    return Promise.resolve({ data: [] })
  }) as never)
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('the address is the search', () => {
  it('reads the old status= and reporter= links, and names the account by id', async () => {
    setRole('admin')
    go({ status: '00000000-0000-4000-8000-0000000000a1', reporter: DANA.id })
    renderWithQuery(<TicketListPage />)

    await waitFor(() => expect(ticketCalls().length).toBeGreaterThan(0))
    expect(lastCall()).toMatchObject({ status_id: ['00000000-0000-4000-8000-0000000000a1'], reporter_id: DANA.id })
    // The account names a set of tickets of its own, so no queue is sent.
    expect(lastCall().scope).toBeUndefined()

    const chips = screen.getByRole('group', { name: 'Active filters' })
    expect(await within(chips).findByText('Dana Whitfield')).toBeTruthy()
    expect(within(chips).getByText('New')).toBeTruthy()
    expect(get).toHaveBeenCalledWith('/users/lookup', { params: { id: DANA.id } })
  })

  it('a copied link reproduces the search, filters and sort included', async () => {
    setRole('staff')
    go({ priority: ['high'], created_from: '2026-10-01', sort: 'subject', order: 'desc', closed: true, page: 2 })
    renderWithQuery(<TicketListPage />)

    await waitFor(() => expect(ticketCalls().length).toBeGreaterThan(0))
    expect(lastCall()).toMatchObject({ priority: ['high'], sort: 'subject', order: 'desc', offset: 50 })
    expect(lastCall().include_closed).toBeUndefined()
    expect(typeof lastCall().created_after).toBe('string')
  })

  it('clearing a chip, and Clear all, write the address', async () => {
    setRole('staff')
    go({ priority: ['high'], status: ['00000000-0000-4000-8000-0000000000a1'] })
    renderWithQuery(<TicketListPage />)

    fireEvent.click(await screen.findByRole('button', { name: 'Remove Priority filter' }))
    expect(router.search).toEqual({ status: ['00000000-0000-4000-8000-0000000000a1'] })

    fireEvent.click(screen.getByRole('button', { name: 'Clear all' }))
    expect(router.search).toEqual({})
    expect(screen.queryByRole('group', { name: 'Active filters' })).toBeNull()
  })

  it('a filter that matches nothing says so and offers Clear all', async () => {
    setRole('staff')
    tickets = []
    go({ priority: ['low'] })
    renderWithQuery(<TicketListPage />)

    await screen.findByText('No tickets match these filters.')
    const clear = screen.getAllByRole('button', { name: 'Clear all' })
    expect(clear.length).toBeGreaterThan(0)
  })
})

describe('the Filters panel', () => {
  it('opens under the toolbar and applies each change at once', async () => {
    setRole('staff')
    renderWithQuery(<TicketListPage />)

    fireEvent.click(await screen.findByRole('button', { name: /^Filters/ }))
    const panel = screen.getByRole('region', { name: 'Filters' })

    fireEvent.click(within(panel).getByLabelText('High'))
    await waitFor(() => expect(lastCall()).toMatchObject({ priority: ['high'] }))
    expect(router.search).toEqual({ priority: ['high'] })

    // The count on the button, and a chip.
    expect(screen.getByRole('button', { name: /^Filters/ }).textContent).toContain('1')
    expect(screen.getByRole('group', { name: 'Active filters' }).textContent).toContain('High')
  })

  it('changing a filter goes back to the first page', async () => {
    setRole('staff')
    go({ page: 3 })
    renderWithQuery(<TicketListPage />)
    fireEvent.click(await screen.findByRole('button', { name: /^Filters/ }))
    fireEvent.click(within(screen.getByRole('region', { name: 'Filters' })).getByLabelText('Low'))
    expect(router.search).toEqual({ priority: ['low'] })
  })

  it('a type needs a category, and picking another category drops it', async () => {
    setRole('staff')
    go({ category: '00000000-0000-4000-8000-0000000000c1', type: '00000000-0000-4000-8000-0000000000d1' })
    renderWithQuery(<TicketListPage />)
    fireEvent.click(await screen.findByRole('button', { name: /^Filters/ }))
    const panel = screen.getByRole('region', { name: 'Filters' })

    await within(panel).findByRole('option', { name: 'Printers' })
    fireEvent.change(within(panel).getByLabelText('Category'), { target: { value: '00000000-0000-4000-8000-0000000000c2' } })
    expect(router.search).toEqual({ category: '00000000-0000-4000-8000-0000000000c2' })
  })

  it('offers the SLA filter when /me says SLA tracking is on', async () => {
    setRole('staff', true)
    renderWithQuery(<TicketListPage />)
    fireEvent.click(await screen.findByRole('button', { name: /^Filters/ }))
    expect(within(screen.getByRole('region', { name: 'Filters' })).getByLabelText('SLA')).toBeTruthy()
  })

  // A ticket with a live SLA is not evidence either way: only /me is asked.
  it.each([
    ['off', false],
    ['absent (unknown)', undefined],
  ])('hides the SLA filter when sla_enabled is %s, even if a ticket carries an SLA', async (_name, enabled) => {
    setRole('staff', enabled)
    tickets = [ticket({ id: 'tk-1', subject: 'Tracked', sla })]
    renderWithQuery(<TicketListPage />)
    await screen.findAllByText('Tracked')
    fireEvent.click(screen.getByRole('button', { name: /^Filters/ }))
    expect(within(screen.getByRole('region', { name: 'Filters' })).queryByLabelText('SLA')).toBeNull()
  })

  it.each([
    ['off', false],
    ['absent (unknown)', undefined],
  ])('hides the SLA column and its sort when sla_enabled is %s', async (_name, enabled) => {
    setRole('staff', enabled)
    tickets = [ticket({ id: 'tk-1', subject: 'Tracked', sla })]
    renderWithQuery(<TicketListPage />)
    await screen.findAllByText('Tracked')
    expect(screen.queryByRole('button', { name: /^SLA/ })).toBeNull()
  })

  it('a requester is offered only what the server will let them use', async () => {
    setRole('user')
    renderWithQuery(<TicketListPage />)
    fireEvent.click(await screen.findByRole('button', { name: /^Filters/ }))
    const panel = screen.getByRole('region', { name: 'Filters' })

    expect(within(panel).getByLabelText('Created from')).toBeTruthy()
    expect(within(panel).getByRole('group', { name: 'Priority' })).toBeTruthy()
    for (const label of ['Assignee', 'Group', 'Requester', 'Last updated from', 'SLA']) {
      expect(within(panel).queryByLabelText(label), label).toBeNull()
    }
    expect(within(panel).queryByText('Came in by')).toBeNull()
  })

  it('a requester\'s link with staff-only filters sends none of them', async () => {
    setRole('user')
    go({ priority: ['high'], assignee: '00000000-0000-4000-8000-0000000000b2', requester: 'whit', channel: ['web'], sla: 'breached', updated_from: '2026-10-01', sort: 'updated' })
    renderWithQuery(<TicketListPage />)
    await waitFor(() => expect(ticketCalls().length).toBeGreaterThan(0))

    const p = lastCall()
    expect(p.priority).toEqual(['high'])
    for (const k of ['assignee_user_id', 'requester', 'source_channel', 'sla', 'updated_after', 'sort']) {
      expect(p[k], k).toBeUndefined()
    }
  })
})

describe('the requester field', () => {
  async function openPanel() {
    setRole('staff')
    renderWithQuery(<TicketListPage />)
    fireEvent.click(await screen.findByRole('button', { name: /^Filters/ }))
    return screen.getByRole('region', { name: 'Filters' })
  }

  it('picking a person sets reporter, and the chip names them', async () => {
    const panel = await openPanel()
    fireEvent.change(within(panel).getByLabelText('Requester'), { target: { value: 'whit' } })

    fireEvent.click(await within(panel).findByRole('button', { name: /Dana Whitfield/ }))
    expect(router.search).toEqual({ reporter: DANA.id })
    await waitFor(() => expect(lastCall()).toMatchObject({ reporter_id: DANA.id }))
    expect(await within(screen.getByRole('group', { name: 'Active filters' })).findByText('Dana Whitfield')).toBeTruthy()
  })

  it('the last option matches text, which also finds guests', async () => {
    const panel = await openPanel()
    fireEvent.change(within(panel).getByLabelText('Requester'), { target: { value: 'whit' } })

    fireEvent.click(await within(panel).findByRole('button', { name: 'Any requester matching “whit”' }))
    expect(router.search).toEqual({ requester: 'whit' })
    await waitFor(() => expect(lastCall()).toMatchObject({ requester: 'whit' }))
    expect(screen.getByRole('group', { name: 'Active filters' }).textContent).toContain('Requester contains')
  })

  it('does not ask the server for one character', async () => {
    const panel = await openPanel()
    fireEvent.change(within(panel).getByLabelText('Requester'), { target: { value: 'w' } })
    await new Promise(r => setTimeout(r, 450))
    expect(get.mock.calls.some((c: unknown[]) => c[0] === '/users/lookup')).toBe(false)
    expect(within(panel).queryByRole('list', { name: 'Requester suggestions' })).toBeNull()
  })
})

describe('sort', () => {
  it('a text search is ranked by relevance until a header is clicked', async () => {
    setRole('staff')
    go({ q: 'printer' })
    renderWithQuery(<TicketListPage />)

    await screen.findByText('Sorted by best match.')
    expect(lastCall().sort).toBeUndefined()
    expect(lastCall()).toMatchObject({ q: 'printer' })
    // No header claims to be the sort.
    expect(document.querySelectorAll('th[aria-sort]').length).toBe(0)
  })

  it('without text the list is newest first, and no sort is sent', async () => {
    setRole('staff')
    renderWithQuery(<TicketListPage />)
    await waitFor(() => expect(ticketCalls().length).toBeGreaterThan(0))
    expect(lastCall().sort).toBeUndefined()
  })

  it('a header sorts in its own direction first, and clicking again reverses', async () => {
    setRole('staff')
    renderWithQuery(<TicketListPage />)
    await screen.findAllByText('Printer jam')

    fireEvent.click(screen.getByRole('button', { name: /^Subject/ }))
    await waitFor(() => expect(lastCall()).toMatchObject({ sort: 'subject', order: 'asc' }))
    expect(router.search).toMatchObject({ sort: 'subject', order: 'asc' })
    expect(screen.getByRole('columnheader', { name: /Subject/ }).getAttribute('aria-sort')).toBe('ascending')

    fireEvent.click(screen.getByRole('button', { name: /^Subject/ }))
    await waitFor(() => expect(lastCall()).toMatchObject({ sort: 'subject', order: 'desc' }))
    expect(screen.getByRole('columnheader', { name: /Subject/ }).getAttribute('aria-sort')).toBe('descending')
  })

  it('dates start newest first, and Created is already the order the list is in', async () => {
    setRole('staff')
    renderWithQuery(<TicketListPage />)
    await screen.findAllByText('Printer jam')
    // No sort in the address means created, newest first: the first click reverses it.
    expect(screen.getByRole('columnheader', { name: /Created/ }).getAttribute('aria-sort')).toBe('descending')
    fireEvent.click(screen.getByRole('button', { name: /^Created/ }))
    await waitFor(() => expect(lastCall()).toMatchObject({ sort: 'created', order: 'asc' }))
    fireEvent.click(screen.getByRole('button', { name: /^Updated/ }))
    await waitFor(() => expect(lastCall()).toMatchObject({ sort: 'updated', order: 'desc' }))
  })

  it('every visible column sorts: ticket, subject, status, priority, SLA, created, updated', async () => {
    setRole('staff', true)
    tickets = [ticket({ id: 'tk-1', subject: 'Tracked', sla })]
    renderWithQuery(<TicketListPage />)
    await screen.findAllByText('Tracked')
    for (const [name, key] of [
      ['Ticket', 'number'],
      ['Subject', 'subject'],
      ['Status', 'status'],
      ['Priority', 'priority'],
      ['SLA', 'sla'],
      ['Created', 'created'],
      ['Updated', 'updated'],
    ] as const) {
      fireEvent.click(screen.getByRole('button', { name: new RegExp(`^${name}`) }))
      await waitFor(() => expect(lastCall().sort, name).toBe(key))
    }
  })

  it('a requester gets no header the server would refuse', async () => {
    setRole('user')
    renderWithQuery(<TicketListPage />)
    await screen.findAllByText('Printer jam')
    expect(screen.queryByRole('button', { name: /^Updated/ })).toBeNull()
    expect(screen.queryByRole('button', { name: /^SLA/ })).toBeNull()
    expect(screen.getByRole('button', { name: /^Priority/ })).toBeTruthy()
  })

  it('the way back to best match drops the sort from the address', async () => {
    setRole('staff')
    go({ q: 'printer', sort: 'subject', order: 'asc' })
    renderWithQuery(<TicketListPage />)

    fireEvent.click(await screen.findByRole('button', { name: 'Sort by best match' }))
    expect(router.search).toEqual({ q: 'printer' })
    await screen.findByText('Sorted by best match.')
  })
})

describe('Search replies', () => {
  it('is on by default and asks for replies only with text', async () => {
    setRole('staff')
    renderWithQuery(<TicketListPage />)
    const box = (await screen.findByLabelText('Search replies')) as HTMLInputElement
    expect(box.checked).toBe(true)

    fireEvent.change(screen.getByPlaceholderText(/Search by tracking number/), { target: { value: 'fuser' } })
    await waitFor(() => expect(lastCall()).toMatchObject({ q: 'fuser', search_replies: true }))
  })

  it('turning it off is kept in the address and stops asking', async () => {
    setRole('staff')
    go({ q: 'fuser' })
    renderWithQuery(<TicketListPage />)
    fireEvent.click(await screen.findByLabelText('Search replies'))
    expect(router.search).toEqual({ q: 'fuser', replies: false })
    await waitFor(() => expect(lastCall().search_replies).toBeUndefined())
  })

  it('the box writes q to the address with replace, after the debounce', async () => {
    setRole('staff')
    renderWithQuery(<TicketListPage />)
    fireEvent.change(await screen.findByPlaceholderText(/Search by tracking number/), { target: { value: 'fuser' } })
    expect(router.search.q).toBeUndefined()
    await waitFor(() => expect(router.search.q).toBe('fuser'))
    expect(router.replaced.at(-1)).toBe(true)
  })
})

describe('reply matches', () => {
  const START = '\u0002'
  const STOP = '\u0003'

  it('shows where the words were and highlights them with <mark>, as text', async () => {
    setRole('staff')
    tickets = [
      ticket({
        id: 'tk-1',
        subject: 'Printer jam',
        reply_match: {
          reply_id: 'r-1',
          internal: true,
          created_at: '2026-11-04T10:00:00Z',
          // Markup in a reply must reach the screen as characters.
          snippet: `…the ${START}fuser${STOP} <img src=x onerror="alert(1)"> is <b>hot</b>…`,
        },
      }),
    ]
    go({ q: 'fuser' })
    renderWithQuery(<TicketListPage />)

    const marks = await screen.findAllByText('fuser')
    expect(marks[0].tagName).toBe('MARK')
    expect(document.querySelector('img')).toBeNull()
    expect(document.querySelector('td b, li b')).toBeNull()
    expect(document.body.textContent).toContain('<img src=x onerror="alert(1)">')
    expect(document.body.textContent).toContain('Internal note')
  })

  it('says nothing about who wrote it', async () => {
    setRole('staff')
    tickets = [
      ticket({
        id: 'tk-1',
        subject: 'Printer jam',
        reply_match: { reply_id: 'r-1', internal: false, created_at: '2026-11-03T10:00:00Z', snippet: `a ${START}b${STOP}` },
      }),
    ]
    go({ q: 'b' })
    renderWithQuery(<TicketListPage />)
    await screen.findAllByText('Public reply', { exact: false })
  })
})

describe('what the server could not search', () => {
  beforeEach(() => {
    setRole('staff')
    go({ q: 'fuser' })
  })

  it('says replies were not fully searched', async () => {
    headers = { 'x-replies-searched': 'false' }
    renderWithQuery(<TicketListPage />)
    await screen.findByText(/Replies weren.t fully searched/)
    expect(screen.queryByText(/still being prepared/)).toBeNull()
  })

  it('says reply search is still being prepared', async () => {
    headers = { 'x-reply-index': 'building' }
    renderWithQuery(<TicketListPage />)
    await screen.findByText(/Reply search is still being prepared/)
  })

  it('says neither when the server says everything was searched', async () => {
    headers = { 'x-replies-searched': 'true' }
    renderWithQuery(<TicketListPage />)
    await screen.findAllByText('Printer jam')
    expect(screen.queryByText(/fully searched/)).toBeNull()
    expect(screen.queryByText(/still being prepared/)).toBeNull()
  })

  it('says neither when the server said nothing: no header is unknown, not a failure', async () => {
    renderWithQuery(<TicketListPage />)
    await screen.findAllByText('Printer jam')
    expect(screen.queryByText(/fully searched/)).toBeNull()
    expect(screen.queryByText(/still being prepared/)).toBeNull()
  })

  it('says neither when replies were not asked for', async () => {
    headers = { 'x-replies-searched': 'false' }
    go({ q: 'fuser', replies: false })
    renderWithQuery(<TicketListPage />)
    await screen.findAllByText('Printer jam')
    expect(screen.queryByText(/fully searched/)).toBeNull()
  })
})

describe('on a phone', () => {
  beforeEach(() => {
    // Below md: the filters are a dialog and nothing is applied until Show results.
    viewport(false)
    setRole('staff')
  })

  it('opens a full-screen dialog and applies the draft together', async () => {
    renderWithQuery(<TicketListPage />)
    fireEvent.click(await screen.findByRole('button', { name: /^Filters/ }))

    const dialog = await screen.findByRole('dialog', { name: 'Filters' })
    fireEvent.click(within(dialog).getByLabelText('High'))
    fireEvent.click(within(dialog).getByLabelText('New'))
    // Nothing yet.
    expect(router.search).toEqual({})

    fireEvent.click(within(dialog).getByRole('button', { name: 'Show results' }))
    expect(router.search).toEqual({ priority: ['high'], status: ['00000000-0000-4000-8000-0000000000a1'] })
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await waitFor(() => expect(lastCall()).toMatchObject({ priority: ['high'], status_id: ['00000000-0000-4000-8000-0000000000a1'] }))
  })

  it('closing it throws the draft away', async () => {
    renderWithQuery(<TicketListPage />)
    fireEvent.click(await screen.findByRole('button', { name: /^Filters/ }))
    const dialog = await screen.findByRole('dialog', { name: 'Filters' })
    fireEvent.click(within(dialog).getByLabelText('High'))
    fireEvent.click(within(dialog).getByRole('button', { name: 'Close filters' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(router.search).toEqual({})
  })

  it('Clear all in the dialog empties the draft, and Show results applies that', async () => {
    go({ priority: ['high'] })
    renderWithQuery(<TicketListPage />)
    fireEvent.click(await screen.findByRole('button', { name: /^Filters/ }))
    const dialog = await screen.findByRole('dialog', { name: 'Filters' })
    expect((within(dialog).getByLabelText('High') as HTMLInputElement).checked).toBe(true)
    fireEvent.click(within(dialog).getByRole('button', { name: 'Clear all' }))
    fireEvent.click(within(dialog).getByRole('button', { name: 'Show results' }))
    expect(router.search).toEqual({})
  })

  it('keeps the rare fields under More filters, and the queue switch stays on the toolbar', async () => {
    setRole('admin')
    renderWithQuery(<TicketListPage />)
    // The existing phone layout test (#414) has the queue switch on the toolbar
    // at this width, so it is not duplicated in the dialog.
    expect(await screen.findByRole('button', { name: 'unassigned' })).toBeTruthy()

    fireEvent.click(screen.getByRole('button', { name: /^Filters/ }))
    const dialog = await screen.findByRole('dialog', { name: 'Filters' })
    expect(within(dialog).queryByRole('button', { name: 'unassigned' })).toBeNull()
    expect(within(dialog).queryByLabelText('Assignee')).toBeNull()
    fireEvent.click(within(dialog).getByRole('button', { name: /^More filters/ }))
    fireEvent.change(await within(dialog).findByLabelText('Assignee'), { target: { value: '00000000-0000-4000-8000-0000000000b2' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Show results' }))
    expect(router.search).toEqual({ assignee: '00000000-0000-4000-8000-0000000000b2' })
  })
})
