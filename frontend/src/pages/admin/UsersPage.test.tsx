import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #449: Admin -> Users showed the newest 200 accounts and nothing else. The
// page now asks the server for 50 at a time, with a name-or-email search and a
// role filter.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/users' } }),
  useNavigate: () => vi.fn(),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

import { UsersPage } from './UsersPage'

function account(n: number) {
  return {
    id: `u-${n}`,
    email: `person${n}@example.com`,
    display_name: `Person ${n}`,
    role: 'user',
    disabled: false,
    auth_type: 'local',
    mfa_enabled: false,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
  }
}

/** The server: 120 accounts, paged by page/per_page, total in X-Total-Count. */
function mockServer(total = 120) {
  const get = vi.spyOn(api, 'get').mockImplementation(((url: string, config?: { params?: Record<string, unknown> }) => {
    if (url === '/admin/users') {
      const p = config?.params ?? {}
      const page = Number(p.page)
      const perPage = Number(p.per_page)
      const from = (page - 1) * perPage
      const rows = Array.from({ length: Math.max(0, Math.min(perPage, total - from)) }, (_, i) => account(from + i + 1))
      return Promise.resolve({ data: rows, headers: { 'x-total-count': String(total) } })
    }
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
  return get
}

function lastUsersParams(get: ReturnType<typeof mockServer>) {
  const calls = get.mock.calls.filter((c) => c[0] === '/admin/users')
  return calls[calls.length - 1][1]?.params as Record<string, unknown>
}

beforeEach(() => {
  vi.restoreAllMocks()
  useAuthStore.setState({
    user: {
      id: 'u-admin',
      email: 'admin@example.com',
      display_name: 'An Admin',
      role: 'admin',
      mfa_enabled: true,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })
})

describe('the users list', () => {
  it('asks for the first page of 50 and says how many there are', async () => {
    const get = mockServer()
    renderWithQuery(<UsersPage />)

    expect(await screen.findByText('Person 1')).toBeTruthy()
    expect(screen.getByText('Person 50')).toBeTruthy()
    expect(screen.queryByText('Person 51')).toBeNull()
    expect(lastUsersParams(get)).toMatchObject({ page: 1, per_page: 50 })
    expect(screen.getByText('Showing 1-50 of 120')).toBeTruthy()
  })

  it('pages forward and back, and stops at the ends', async () => {
    const get = mockServer()
    const user = userEvent.setup()
    renderWithQuery(<UsersPage />)
    await screen.findByText('Person 1')

    const prev = screen.getByRole('button', { name: 'Previous' }) as HTMLButtonElement
    const next = screen.getByRole('button', { name: 'Next' }) as HTMLButtonElement
    expect(prev.disabled).toBe(true)

    await user.click(next)
    expect(await screen.findByText('Person 51')).toBeTruthy()
    expect(lastUsersParams(get)).toMatchObject({ page: 2 })
    expect(screen.getByText('Showing 51-100 of 120')).toBeTruthy()

    await user.click(next)
    expect(await screen.findByText('Person 101')).toBeTruthy()
    expect(screen.getByText('Showing 101-120 of 120')).toBeTruthy()
    expect((screen.getByRole('button', { name: 'Next' }) as HTMLButtonElement).disabled).toBe(true)

    await user.click(screen.getByRole('button', { name: 'Previous' }))
    expect(await screen.findByText('Person 51')).toBeTruthy()
  })

  it('searches by name or email and goes back to page 1', async () => {
    const get = mockServer()
    const user = userEvent.setup()
    renderWithQuery(<UsersPage />)
    await screen.findByText('Person 1')
    await user.click(screen.getByRole('button', { name: 'Next' }))
    await screen.findByText('Person 51')

    await user.type(screen.getByRole('searchbox', { name: 'Search users' }), 'grace{enter}')

    await waitFor(() => expect(lastUsersParams(get)).toMatchObject({ page: 1, q: 'grace' }))
  })

  it('filters by role and goes back to page 1', async () => {
    const get = mockServer()
    const user = userEvent.setup()
    renderWithQuery(<UsersPage />)
    await screen.findByText('Person 1')
    await user.click(screen.getByRole('button', { name: 'Next' }))
    await screen.findByText('Person 51')

    await user.selectOptions(screen.getByRole('combobox', { name: 'Filter by role' }), 'staff')

    await waitFor(() => expect(lastUsersParams(get)).toMatchObject({ page: 1, role: 'staff' }))
  })

  it('sends no q or role until the admin sets them', async () => {
    const get = mockServer()
    renderWithQuery(<UsersPage />)
    await screen.findByText('Person 1')
    const p = lastUsersParams(get)
    expect(p).not.toHaveProperty('q')
    expect(p).not.toHaveProperty('role')
  })

  it('says so when nothing matches', async () => {
    mockServer(0)
    renderWithQuery(<UsersPage />)
    expect(await screen.findByText('No users found.')).toBeTruthy()
    expect(screen.queryByText(/Showing/)).toBeNull()
  })
})
