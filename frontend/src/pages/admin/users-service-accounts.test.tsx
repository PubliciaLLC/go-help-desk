import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// Service users (#420): the identity an OAuth client acts as. They are listed
// on Admin -> Users, badged, and can be filtered. The filter is the server's
// service_account parameter on the paged list, so the count and the pages follow it.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/users' } }),
  useNavigate: () => vi.fn(),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

import { UsersPage } from './UsersPage'

function row(over: Record<string, unknown>) {
  return {
    role: 'staff',
    disabled: false,
    auth_type: 'local',
    service_account: false,
    mfa_enabled: false,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...over,
  }
}

const PERSON = row({ id: 'u-1', email: 'ann@example.com', display_name: 'Ann Person' })
const SERVICE = row({
  id: 'u-2',
  email: 'oauth-1@service-account.invalid',
  display_name: 'JIRA sync',
  auth_type: 'service',
  service_account: true,
})

// A server that filters like GET /admin/users does: service_account=true keeps
// service accounts, false keeps people, absent keeps everyone, and
// X-Total-Count counts the filtered list. Pages hold 50.
function mockUsers(users: Array<Record<string, unknown>>) {
  return vi.spyOn(api, 'get').mockImplementation(((url: string, config?: { params?: Record<string, unknown> }) => {
    if (url === '/admin/users') {
      const p = config?.params ?? {}
      const kept = users.filter((u) =>
        p.service_account === undefined ? true : u.service_account === p.service_account,
      )
      const from = (Number(p.page) - 1) * Number(p.per_page)
      return Promise.resolve({
        data: kept.slice(from, from + Number(p.per_page)),
        headers: { 'x-total-count': String(kept.length) },
      })
    }
    return Promise.resolve({ data: {} })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

function usersParams(get: ReturnType<typeof mockUsers>) {
  const calls = get.mock.calls.filter((c) => c[0] === '/admin/users')
  return calls[calls.length - 1][1]?.params as Record<string, unknown>
}

beforeEach(() => {
  vi.restoreAllMocks()
  useAuthStore.setState({
    user: {
      id: 'u-0', email: 'admin@example.com', display_name: 'An Admin', role: 'admin',
      mfa_enabled: true, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
  mockUsers([PERSON, SERVICE])
})

describe('service users on Admin -> Users', () => {
  it('badges a service user and nobody else', async () => {
    renderWithQuery(<UsersPage />)
    const svcRow = (await screen.findByText('JIRA sync')).closest('tr')!
    expect(within(svcRow).getByText('Service')).toBeTruthy()
    const personRow = screen.getByText('Ann Person').closest('tr')!
    expect(within(personRow).queryByText('Service')).toBeNull()
  })

  it('does not call a service user a local login', async () => {
    renderWithQuery(<UsersPage />)
    const svcRow = (await screen.findByText('JIRA sync')).closest('tr')!
    expect(within(svcRow).queryByText('Local')).toBeNull()
  })

  it('asks the server for everyone, people only, or service accounts only', async () => {
    const user = userEvent.setup()
    const get = mockUsers([PERSON, SERVICE])
    renderWithQuery(<UsersPage />)
    await screen.findByText('JIRA sync')
    expect(usersParams(get)).not.toHaveProperty('service_account')
    const filter = screen.getByRole('combobox', { name: 'Show' })

    await user.selectOptions(filter, 'people')
    await waitFor(() => expect(usersParams(get).service_account).toBe(false))
    await waitFor(() => expect(screen.queryByText('JIRA sync')).toBeNull())
    expect(screen.getByText('Ann Person')).toBeTruthy()

    await user.selectOptions(filter, 'service')
    await waitFor(() => expect(usersParams(get).service_account).toBe(true))
    await waitFor(() => expect(screen.queryByText('Ann Person')).toBeNull())
    expect(screen.getByText('JIRA sync')).toBeTruthy()

    await user.selectOptions(filter, 'all')
    await waitFor(() => expect(usersParams(get)).not.toHaveProperty('service_account'))
    expect(await screen.findByText('Ann Person')).toBeTruthy()
  })

  it('goes back to page 1 when the filter changes', async () => {
    const user = userEvent.setup()
    const many = Array.from({ length: 120 }, (_, i) =>
      row({ id: `p-${i}`, email: `p${i}@example.com`, display_name: `Person ${i}` }),
    )
    const get = mockUsers([...many, SERVICE])
    renderWithQuery(<UsersPage />)
    await screen.findByText('Person 0')
    await user.click(screen.getByRole('button', { name: 'Next' }))
    await waitFor(() => expect(usersParams(get).page).toBe(2))

    await user.selectOptions(screen.getByRole('combobox', { name: 'Show' }), 'service')
    await waitFor(() => expect(usersParams(get)).toMatchObject({ page: 1, service_account: true }))
  })

  it('counts and pages by the filtered list', async () => {
    const user = userEvent.setup()
    const many = Array.from({ length: 120 }, (_, i) =>
      row({ id: `p-${i}`, email: `p${i}@example.com`, display_name: `Person ${i}` }),
    )
    mockUsers([...many, SERVICE])
    renderWithQuery(<UsersPage />)
    expect(await screen.findByText('Showing 1-50 of 121')).toBeTruthy()

    await user.selectOptions(screen.getByRole('combobox', { name: 'Show' }), 'service')
    expect(await screen.findByText('Showing 1-1 of 1')).toBeTruthy()
    expect((screen.getByRole('button', { name: 'Next' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('has no "this page only" caveat any more', async () => {
    const user = userEvent.setup()
    mockUsers([PERSON, SERVICE])
    renderWithQuery(<UsersPage />)
    await screen.findByText('JIRA sync')
    await user.selectOptions(screen.getByRole('combobox', { name: 'Show' }), 'service')
    expect(screen.queryByText(/this page only/i)).toBeNull()
  })

  it('says so when the filter matches nobody', async () => {
    const user = userEvent.setup()
    mockUsers([PERSON])
    renderWithQuery(<UsersPage />)
    await screen.findByText('Ann Person')
    await user.selectOptions(screen.getByRole('combobox', { name: 'Show' }), 'service')
    expect(await screen.findByText('No users found.')).toBeTruthy()
  })
})
