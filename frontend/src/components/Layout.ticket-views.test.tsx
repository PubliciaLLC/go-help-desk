import { describe, expect, it, vi, beforeEach } from 'vitest'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import type { User } from '@/api/types'

// #676: staff and administrators see the ticket list's views in the sidebar,
// under a fixed Tickets group; a requester keeps the single Tickets link. The
// drawer is the same SidebarContent, so its copy lists the same items.

const router = vi.hoisted(() => ({
  pathname: '/dashboard',
  search: {} as Record<string, unknown>,
}))

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: router.pathname, search: router.search } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, search, children, ...rest }: any) => (
    <a href={search ? `${to}?${new URLSearchParams(search).toString()}` : to} {...rest}>
      {children}
    </a>
  ),
}))

import { Layout } from './Layout'

function signInAs(role: User['role']) {
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'someone@example.com', display_name: 'Someone', role, mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
}

// What GET /me answers. `me` is the promise for the answer, so a test can hold
// it back to see the sidebar before /me has answered.
let meAnswer: Promise<{ data: unknown }> = Promise.resolve({ data: {} })

function setMe(data: Record<string, unknown>) {
  meAnswer = Promise.resolve({ data })
}

function render(role: User['role'], opts: { pathname?: string; search?: Record<string, unknown> } = {}) {
  router.pathname = opts.pathname ?? '/tickets'
  router.search = opts.search ?? {}
  signInAs(role)
  return renderWithQuery(<Layout>content</Layout>)
}

const TICKET_VIEWS = [
  ['Assigned to Me', '/tickets?view=me'],
  ["My Group's Tickets", '/tickets?view=groups'],
  ['Available', '/tickets?view=available'],
  ['Search', '/tickets?view=search'],
] as const

// [label, href] of every link in one copy of the sidebar, in document order.
function linksIn(root: Element) {
  return Array.from(root.querySelectorAll('a')).map((a) => [a.textContent, a.getAttribute('href')])
}

function ticketsGroup() {
  return screen.getByRole('group', { name: 'Tickets' })
}

beforeEach(() => {
  vi.restoreAllMocks()
  meAnswer = Promise.resolve({ data: {} })
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(api, 'get').mockImplementation(((url: string) => (url === '/me' ? meAnswer : Promise.resolve({ data: {} }))) as any)
})

describe('the Tickets views per role', () => {
  it('an administrator sees all four views, Available without waiting for /me', () => {
    render('admin')

    const names = within(ticketsGroup()).getAllByRole('link')
    expect(names.map((a) => [a.textContent, a.getAttribute('href')])).toEqual(TICKET_VIEWS)
    expect(screen.queryByRole('link', { name: 'Tickets' })).toBeNull()
  })

  it('staff with the setting on see all four views', async () => {
    setMe({ id: 'u-1', role: 'staff', can_use_available_queue: true })
    const { queryClient } = render('staff')

    await waitFor(() => expect(queryClient.getQueryState(['me'])?.status).toBe('success'))
    expect(within(ticketsGroup()).getAllByRole('link').map((a) => [a.textContent, a.getAttribute('href')]))
      .toEqual(TICKET_VIEWS)
  })

  it('staff with the setting off see Assigned to Me, My Group\'s Tickets and Search, not Available', async () => {
    setMe({ id: 'u-1', role: 'staff', can_use_available_queue: false })
    const { queryClient } = render('staff')

    await waitFor(() => expect(queryClient.getQueryState(['me'])?.status).toBe('success'))
    expect(within(ticketsGroup()).getAllByRole('link').map((a) => a.textContent))
      .toEqual(['Assigned to Me', "My Group's Tickets", 'Search'])
  })

  it('a requester keeps the single Tickets link to their tickets, and asks /me for nothing', () => {
    render('user')

    expect(screen.queryByRole('group', { name: 'Tickets' })).toBeNull()
    expect(screen.getByRole('link', { name: 'Tickets' }).getAttribute('href')).toBe('/tickets')
    expect(api.get).not.toHaveBeenCalledWith('/me')
  })

  it('staff ask /me, and an administrator does not need to', () => {
    render('staff')
    expect(api.get).toHaveBeenCalledWith('/me')
  })
})

describe('Available for staff waits for /me', () => {
  it('is not shown until /me has answered, then is shown when the setting allows it', async () => {
    let answer: (v: { data: unknown }) => void = () => {}
    meAnswer = new Promise((resolve) => { answer = resolve })
    render('staff')

    expect(screen.queryByRole('link', { name: 'Available' })).toBeNull()
    expect(within(ticketsGroup()).getAllByRole('link').map((a) => a.textContent))
      .toEqual(['Assigned to Me', "My Group's Tickets", 'Search'])

    await act(async () => {
      answer({ data: { id: 'u-1', role: 'staff', can_use_available_queue: true } })
    })
    expect(await screen.findByRole('link', { name: 'Available' })).toBeTruthy()
  })
})

describe('the current view link', () => {
  it.each([
    ['groups', { view: 'groups' }, "My Group's Tickets"],
    ['me', { view: 'me' }, 'Assigned to Me'],
    ['search', { view: 'search' }, 'Search'],
  ])('is the %s view when the address names it', (_name, search, label) => {
    render('admin', { pathname: '/tickets', search })

    const current = within(ticketsGroup()).getAllByRole('link').filter((a) => a.getAttribute('aria-current') === 'page')
    expect(current.map((a) => a.textContent)).toEqual([label])
    expect(current[0].className).toContain('text-blue-700')
  })

  it('no view is current on /tickets without a view, or on a ticket page', () => {
    const { unmount } = render('admin', { pathname: '/tickets', search: {} })
    expect(within(ticketsGroup()).getAllByRole('link').filter((a) => a.hasAttribute('aria-current'))).toEqual([])
    unmount()

    render('admin', { pathname: '/tickets/abc-123', search: { view: 'me' } })
    expect(within(ticketsGroup()).getAllByRole('link').filter((a) => a.hasAttribute('aria-current'))).toEqual([])
  })

  it('is not current anywhere else in the app', () => {
    render('admin', { pathname: '/dashboard', search: { view: 'me' } })
    expect(within(ticketsGroup()).getAllByRole('link').filter((a) => a.hasAttribute('aria-current'))).toEqual([])
  })
})

describe('the links', () => {
  it('are at least 44 px tall on a coarse pointer', () => {
    render('admin')
    for (const a of within(ticketsGroup()).getAllByRole('link')) {
      expect(a.className, a.textContent ?? '').toContain('pointer-coarse:min-h-11')
    }
  })
})

describe('the drawer copy', () => {
  it('lists the same links as the permanent sidebar, and following one closes the drawer', () => {
    render('admin')
    const aside = document.querySelector('aside') as HTMLElement
    const sidebar = linksIn(aside)

    fireEvent.click(screen.getByRole('button', { name: 'Open navigation menu' }))
    const dialog = screen.getByRole('dialog')
    expect(linksIn(dialog)).toEqual(sidebar)
    expect(sidebar).toContainEqual(['Search', '/tickets?view=search'])

    fireEvent.click(within(dialog).getByRole('link', { name: 'Search' }))
    expect(screen.queryByRole('dialog')).toBeNull()
  })
})
