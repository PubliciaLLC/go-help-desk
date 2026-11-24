import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #676: the ticket list's views, the translation of an address made before
// them, and the control that splits My Group's Tickets by who holds the ticket.
// The address is real here (navigate() writes it, useSearch() reads it back).

const router = vi.hoisted(() => ({
  search: {} as Record<string, unknown>,
  listeners: new Set<() => void>(),
  writes: [] as { search: Record<string, unknown>; replace: boolean }[],
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
          router.writes.push({ search: router.search, replace: Boolean(opts.replace) })
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

const ID_STATUS = '00000000-0000-4000-8000-0000000000a1'
const ID_PERSON = '00000000-0000-4000-8000-0000000000b2'
const ID_GROUP = '00000000-0000-4000-8000-0000000000e1'

let tickets: unknown[]
let meAnswer: unknown
let meCalls: number
let get: ReturnType<typeof vi.spyOn>

function ticketCalls(): Record<string, unknown>[] {
  return get.mock.calls
    .filter((c: unknown[]) => c[0] === '/tickets')
    .map((c: unknown[]) => (c[1] as { params: Record<string, unknown> }).params)
}
const lastCall = () => ticketCalls().at(-1)!

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

function go(search: Record<string, unknown>) {
  router.search = search
}

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

beforeEach(() => {
  viewport(true)
  router.search = {}
  router.listeners.clear()
  router.writes = []
  tickets = []
  meAnswer = { can_use_available_queue: false }
  meCalls = 0
  get = vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/tickets') return Promise.resolve({ data: tickets, headers: {} })
    if (url === '/me') {
      meCalls++
      return Promise.resolve({ data: meAnswer })
    }
    if (url === '/statuses') return Promise.resolve({ data: [{ id: ID_STATUS, name: 'New', kind: 'system', sort_order: 1, color: '#3b82f6', active: true, ticket_count: 0 }] })
    if (url === '/staff') return Promise.resolve({ data: [{ id: ID_PERSON, display_name: 'Sam Staff', assignable: true }] })
    if (url === '/groups') return Promise.resolve({ data: [{ id: ID_GROUP, name: 'Network' }] })
    return Promise.resolve({ data: [] })
  }) as never)
})

afterEach(() => {
  vi.unstubAllGlobals()
})

const heading = () => screen.getByRole('heading', { level: 1 }).textContent

describe('the address is translated once, after /me has answered', () => {
  it.each([
    ['nothing', 'staff', {}, { view: 'me' }],
    ['scope=mine', 'staff', { scope: 'mine' }, { view: 'me' }],
    ['a dashboard filter link', 'staff', { status: ID_STATUS }, { view: 'search', status: [ID_STATUS] }],
    ['text', 'staff', { q: 'printer' }, { view: 'search', q: 'printer' }],
    ['scope=all', 'admin', { scope: 'all' }, { view: 'search' }],
    ['scope=unassigned for an administrator', 'admin', { scope: 'unassigned' }, { view: 'available' }],
    ['scope=unassigned for staff the setting leaves out', 'staff', { scope: 'unassigned' }, { view: 'me' }],
    ['view=available for staff the setting leaves out', 'staff', { view: 'available' }, { view: 'me' }],
    ['view=groups with held_by', 'staff', { view: 'groups', held_by: 'me' }, { view: 'groups', held_by: 'me' }],
  ] as const)('%s becomes %j', async (_name, role, from, to) => {
    setRole(role)
    go({ ...from })
    renderWithQuery(<TicketListPage />)

    await waitFor(() => expect(router.search).toEqual(to))
    // Replaced, not added: Back must not return to the address that was translated.
    expect(router.writes.every(w => w.replace)).toBe(true)
  })

  it('does nothing until /me has answered', async () => {
    setRole('staff')
    let answer: (v: unknown) => void = () => {}
    get.mockImplementation(((url: string) => {
      if (url === '/me') return new Promise(r => (answer = r))
      return Promise.resolve({ data: [], headers: {} })
    }) as never)
    go({})
    renderWithQuery(<TicketListPage />)
    await waitFor(() => expect(get.mock.calls.some((c: unknown[]) => c[0] === '/me')).toBe(true))
    expect(router.writes).toEqual([])
    expect(ticketCalls()).toEqual([])

    answer({ data: { can_use_available_queue: false } })
    await waitFor(() => expect(router.search).toEqual({ view: 'me' }))
  })

  it('writes nothing when the address already carries a view', async () => {
    setRole('staff')
    go({ view: 'groups' })
    renderWithQuery(<TicketListPage />)
    await waitFor(() => expect(ticketCalls().length).toBeGreaterThan(0))
    expect(router.writes).toEqual([])
  })

  it('an administrator keeps view=available whatever /me says', async () => {
    setRole('admin')
    meAnswer = { can_use_available_queue: false }
    go({ view: 'available' })
    renderWithQuery(<TicketListPage />)
    await waitFor(() => expect(lastCall()).toMatchObject({ scope: 'unassigned' }))
    expect(router.search).toEqual({ view: 'available' })
  })

  it('staff the setting lists keep view=available', async () => {
    setRole('staff')
    meAnswer = { can_use_available_queue: true }
    go({ view: 'available' })
    renderWithQuery(<TicketListPage />)
    await waitFor(() => expect(lastCall()).toMatchObject({ scope: 'unassigned' }))
    expect(router.search).toEqual({ view: 'available' })
    expect(heading()).toBe('Available')
  })

  it("a requester's view, scope and held_by are dropped, and they ask for what they always did", async () => {
    setRole('user')
    go({ view: 'groups', held_by: 'me', scope: 'all', q: 'printer' })
    renderWithQuery(<TicketListPage />)

    await waitFor(() => expect(router.search).toEqual({ q: 'printer' }))
    await waitFor(() => expect(lastCall()).toMatchObject({ scope: 'mine', q: 'printer' }))
    expect(lastCall()).not.toHaveProperty('held_by')
    expect(meCalls).toBe(0)
    expect(heading()).toBe('Tickets')
  })
})

describe('what each view asks for, and what the heading says', () => {
  it.each([
    ['me', 'Assigned to Me', 'assigned_to_me'],
    ['groups', "My Group's Tickets", 'my_groups'],
    ['available', 'Available', 'unassigned'],
    ['search', 'Search', 'visible'],
  ] as const)('view=%s is headed %s and sends scope=%s', async (view, name, scope) => {
    setRole('admin')
    go({ view })
    renderWithQuery(<TicketListPage />)
    await waitFor(() => expect(lastCall()).toMatchObject({ scope }))
    expect(heading()).toBe(name)
  })

  it('the Mine / Unassigned / All buttons and the Only <queue> chip are gone', async () => {
    setRole('admin')
    go({ view: 'me', q: 'printer' })
    renderWithQuery(<TicketListPage />)
    await waitFor(() => expect(ticketCalls().length).toBeGreaterThan(0))
    for (const name of ['mine', 'unassigned', 'all']) expect(screen.queryByRole('button', { name })).toBeNull()
    expect(screen.queryByRole('button', { name: /^Only/ })).toBeNull()
    // Typing in a queue stays in the queue.
    expect(lastCall()).toMatchObject({ q: 'printer', scope: 'assigned_to_me' })
  })

  it('Search says what it covers, and the other views do not', async () => {
    setRole('staff')
    go({ view: 'search' })
    const { unmount } = renderWithQuery(<TicketListPage />)
    expect(await screen.findByText('Searching all tickets you can see')).toBeTruthy()
    unmount()

    go({ view: 'me' })
    renderWithQuery(<TicketListPage />)
    await waitFor(() => expect(ticketCalls().length).toBeGreaterThan(0))
    expect(screen.queryByText('Searching all tickets you can see')).toBeNull()
  })

  it('a group named inside My Group\'s Tickets narrows it: the scope is still sent', async () => {
    setRole('staff')
    go({ view: 'groups', group: ID_GROUP })
    renderWithQuery(<TicketListPage />)
    await waitFor(() => expect(lastCall()).toMatchObject({ scope: 'my_groups', assignee_group_id: ID_GROUP }))
  })

  it('a requester filter inside Assigned to Me narrows it: the scope is still sent', async () => {
    setRole('staff')
    go({ view: 'me', requester: 'whit' })
    renderWithQuery(<TicketListPage />)
    await waitFor(() => expect(lastCall()).toMatchObject({ scope: 'assigned_to_me', requester: 'whit' }))
  })
})

describe("the My Group's Tickets choice", () => {
  const choice = () => screen.getByRole('group', { name: 'Who holds the ticket' })

  it('is four buttons, All pressed when held_by is absent', async () => {
    setRole('staff')
    go({ view: 'groups' })
    renderWithQuery(<TicketListPage />)
    await waitFor(() => expect(ticketCalls().length).toBeGreaterThan(0))

    const names = within(choice()).getAllByRole('button').map(b => b.textContent)
    expect(names).toEqual(['All', 'Group queue', 'Assigned to me', 'Assigned to others'])
    expect(within(choice()).getByRole('button', { name: 'All' }).getAttribute('aria-pressed')).toBe('true')
    expect(within(choice()).getByRole('button', { name: 'Group queue' }).getAttribute('aria-pressed')).toBe('false')
  })

  it('wraps, and is 44 px tall on a coarse pointer', async () => {
    setRole('staff')
    go({ view: 'groups' })
    renderWithQuery(<TicketListPage />)
    await waitFor(() => expect(ticketCalls().length).toBeGreaterThan(0))
    expect(choice().className).toContain('flex-wrap')
    for (const b of within(choice()).getAllByRole('button')) expect(b.className).toContain('pointer-coarse:min-h-11')
  })

  it.each([
    ['Group queue', 'nobody'],
    ['Assigned to me', 'me'],
    ['Assigned to others', 'others'],
  ] as const)('%s writes held_by=%s, sends it, and starts at the first page', async (label, value) => {
    setRole('staff')
    go({ view: 'groups', page: 3 })
    renderWithQuery(<TicketListPage />)
    await waitFor(() => expect(ticketCalls().length).toBeGreaterThan(0))

    fireEvent.click(within(choice()).getByRole('button', { name: label }))
    expect(router.search).toEqual({ view: 'groups', held_by: value })
    await waitFor(() => expect(lastCall()).toMatchObject({ scope: 'my_groups', held_by: value, offset: 0 }))
    expect(within(choice()).getByRole('button', { name: label }).getAttribute('aria-pressed')).toBe('true')
    expect(within(choice()).getByRole('button', { name: 'All' }).getAttribute('aria-pressed')).toBe('false')
  })

  it('All removes held_by', async () => {
    setRole('staff')
    go({ view: 'groups', held_by: 'others' })
    renderWithQuery(<TicketListPage />)
    await waitFor(() => expect(lastCall()).toMatchObject({ held_by: 'others' }))

    fireEvent.click(within(choice()).getByRole('button', { name: 'All' }))
    expect(router.search).toEqual({ view: 'groups' })
    await waitFor(() => expect(lastCall()).not.toHaveProperty('held_by'))
  })

  it('is on My Group\'s Tickets only, and a requester never sees it', async () => {
    for (const [role, view] of [['admin', 'me'], ['admin', 'available'], ['admin', 'search'], ['user', undefined]] as const) {
      setRole(role)
      go(view ? { view } : {})
      const { unmount } = renderWithQuery(<TicketListPage />)
      await waitFor(() => expect(ticketCalls().length).toBeGreaterThan(0))
      expect(screen.queryByRole('group', { name: 'Who holds the ticket' }), `${role} ${view}`).toBeNull()
      unmount()
      get.mockClear()
    }
  })
})

describe('empty states', () => {
  it.each([
    ['staff', { view: 'me' }, 'No tickets are assigned to you.'],
    ['staff', { view: 'groups' }, 'No tickets are assigned to your groups.'],
    ['staff', { view: 'groups', held_by: 'nobody' }, 'No group tickets are waiting for someone to take them.'],
    ['staff', { view: 'groups', held_by: 'me' }, "None of your groups' tickets are assigned to you."],
    ['staff', { view: 'groups', held_by: 'others' }, "None of your groups' tickets are held by a teammate."],
    ['admin', { view: 'available' }, 'No tickets are waiting without a group or a person.'],
    ['staff', { view: 'search' }, 'No tickets yet.'],
    ['user', {}, 'No tickets are currently assigned to you or your groups.'],
  ] as const)('%s on %j says %s', async (role, search, message) => {
    setRole(role)
    go({ ...search })
    renderWithQuery(<TicketListPage />)
    expect(await screen.findByText(message)).toBeTruthy()
  })

  it.each([
    ['me', { view: 'me', q: 'zzz' }],
    ['groups', { view: 'groups', q: 'zzz' }],
    ['groups held by me', { view: 'groups', held_by: 'me', q: 'zzz' }],
    ['available', { view: 'available', q: 'zzz' }],
    ['search', { view: 'search', q: 'zzz' }],
  ] as const)('text on %s says nothing matches the search', async (_name, search) => {
    setRole('admin')
    go({ ...search })
    renderWithQuery(<TicketListPage />)
    expect(await screen.findByText('No tickets match your search.')).toBeTruthy()
  })

  it('a filter on a view says nothing matches, with the way out', async () => {
    setRole('staff')
    go({ view: 'me', priority: ['low'] })
    renderWithQuery(<TicketListPage />)
    expect(await screen.findByText('No tickets match these filters.')).toBeTruthy()
  })
})

describe('filters a view does not offer', () => {
  const FULL = { assignee: ID_PERSON, group: ID_GROUP, requester: 'whit', priority: ['high'] }

  it('Available shows no chip for them, does not send them, and keeps the ones it offers', async () => {
    setRole('admin')
    go({ view: 'available', ...FULL })
    renderWithQuery(<TicketListPage />)
    await waitFor(() => expect(lastCall()).toMatchObject({ scope: 'unassigned', priority: ['high'] }))
    for (const k of ['assignee_user_id', 'assignee_group_id', 'reporter_id', 'requester']) expect(lastCall(), k).not.toHaveProperty(k)

    const chips = screen.getByRole('group', { name: 'Active filters' })
    expect(within(chips).getByText('High')).toBeTruthy()
    for (const label of ['Assignee', 'Group', 'Requester']) {
      expect(within(chips).queryByText(new RegExp(`^${label}`))).toBeNull()
    }
    // One chip, one in the badge.
    expect(screen.getByRole('button', { name: /^Filters/ }).textContent).toContain('1')
  })

  it('Assigned to Me leaves out Assignee only', async () => {
    setRole('staff')
    go({ view: 'me', ...FULL })
    renderWithQuery(<TicketListPage />)
    await waitFor(() => expect(lastCall()).toMatchObject({ scope: 'assigned_to_me', assignee_group_id: ID_GROUP, requester: 'whit' }))
    expect(lastCall()).not.toHaveProperty('assignee_user_id')
    const chips = screen.getByRole('group', { name: 'Active filters' })
    expect(within(chips).queryByText(/^Assignee/)).toBeNull()
    expect(within(chips).getByText(/^Group/)).toBeTruthy()
  })

  it('My Group\'s Tickets and Search offer all of them', async () => {
    for (const view of ['groups', 'search']) {
      setRole('staff')
      go({ view, ...FULL })
      const { unmount } = renderWithQuery(<TicketListPage />)
      await waitFor(() => expect(lastCall()).toMatchObject({ assignee_user_id: ID_PERSON, assignee_group_id: ID_GROUP, requester: 'whit' }))
      unmount()
      get.mockClear()
    }
  })

  it('the desktop panel does not offer them', async () => {
    setRole('admin')
    go({ view: 'available' })
    renderWithQuery(<TicketListPage />)
    fireEvent.click(await screen.findByRole('button', { name: /^Filters/ }))
    const panel = screen.getByRole('region', { name: 'Filters' })
    expect(within(panel).getByLabelText('Category')).toBeTruthy()
    for (const label of ['Assignee', 'Group', 'Requester']) expect(within(panel).queryByLabelText(label)).toBeNull()
  })

  it('the panel on Assigned to Me offers Group and Requester but not Assignee', async () => {
    setRole('staff')
    go({ view: 'me' })
    renderWithQuery(<TicketListPage />)
    fireEvent.click(await screen.findByRole('button', { name: /^Filters/ }))
    const panel = screen.getByRole('region', { name: 'Filters' })
    expect(within(panel).queryByLabelText('Assignee')).toBeNull()
    expect(within(panel).getByLabelText('Group')).toBeTruthy()
    expect(within(panel).getByLabelText('Requester')).toBeTruthy()
  })

  it('the phone dialog does not offer them either, and its More filters line does not name them', async () => {
    viewport(false)
    setRole('admin')
    go({ view: 'available' })
    renderWithQuery(<TicketListPage />)
    fireEvent.click(await screen.findByRole('button', { name: /^Filters/ }))
    const dialog = await screen.findByRole('dialog', { name: 'Filters' })
    const more = within(dialog).getByRole('button', { name: /^More filters/ })
    expect(more.textContent).not.toMatch(/assignee|group/)
    expect(within(dialog).queryByLabelText('Requester')).toBeNull()
    fireEvent.click(more)
    expect(within(dialog).queryByLabelText('Assignee')).toBeNull()
    expect(within(dialog).queryByLabelText('Group')).toBeNull()
    expect(within(dialog).getByLabelText('Item')).toBeTruthy()
  })
})
