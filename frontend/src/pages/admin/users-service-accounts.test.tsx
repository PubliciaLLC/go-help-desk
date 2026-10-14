import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// Service users (#420): the identity an OAuth client acts as. They are listed
// on Admin -> Users, badged, and can be filtered. The filter is client-side on
// service_account; GET /admin/users is still limit/offset with a 200 default,
// so it filters the page that was loaded until paging (#467) lands.

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

function mockUsers(users: unknown[]) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/users') return Promise.resolve({ data: users })
    return Promise.resolve({ data: {} })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
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

  it('shows everyone by default, people only, or service accounts only', async () => {
    const user = userEvent.setup()
    renderWithQuery(<UsersPage />)
    await screen.findByText('JIRA sync')
    const filter = screen.getByRole('combobox', { name: 'Show' })
    expect(screen.getByText('Ann Person')).toBeTruthy()

    await user.selectOptions(filter, 'people')
    expect(screen.getByText('Ann Person')).toBeTruthy()
    expect(screen.queryByText('JIRA sync')).toBeNull()

    await user.selectOptions(filter, 'service')
    expect(screen.queryByText('Ann Person')).toBeNull()
    expect(screen.getByText('JIRA sync')).toBeTruthy()

    await user.selectOptions(filter, 'all')
    expect(screen.getByText('Ann Person')).toBeTruthy()
    expect(screen.getByText('JIRA sync')).toBeTruthy()
  })

  it('says so when the filter matches nobody', async () => {
    const user = userEvent.setup()
    mockUsers([PERSON])
    renderWithQuery(<UsersPage />)
    await screen.findByText('Ann Person')
    await user.selectOptions(screen.getByRole('combobox', { name: 'Show' }), 'service')
    expect(screen.getByText('No users found.')).toBeTruthy()
  })
})
