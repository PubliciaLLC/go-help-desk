import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError, AxiosHeaders } from 'axios'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// A service user's detail page (#420). It never has a password, a second
// factor or the admin role, so the page offers none of them. It can be
// renamed, given groups, set to staff or user, and disabled. It can only be
// deleted by deleting its OAuth client, and the server says so with a 409.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/users/u-2' } }),
  useNavigate: () => vi.fn(),
  useParams: () => ({ id: 'u-2' }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

import { UserDetailPage } from './UserDetailPage'

const SERVICE = {
  id: 'u-2',
  email: 'oauth-1@service-account.invalid',
  display_name: 'JIRA sync',
  role: 'staff',
  disabled: false,
  // Defensive: the database forbids a service user from holding any of these,
  // but the page must hide the controls because it IS a service user, not
  // because the flags happen to be empty.
  has_password: true,
  mfa_enabled: true,
  passkey_count: 1,
  auth_type: 'service',
  service_account: true,
  groups: [],
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
}

const DELETE_MESSAGE =
  'This is the service account for OAuth client JIRA sync. Delete the client instead.'

function conflict() {
  const headers = new AxiosHeaders()
  return new AxiosError('Request failed', 'ERR_BAD_REQUEST', { headers } as never, null, {
    status: 409, statusText: 'Conflict', headers, config: { headers } as never,
    data: { error: { code: 'service_account', message: DELETE_MESSAGE } },
  } as never)
}

function mockDetail(detail: Record<string, unknown>) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/users/u-2') return Promise.resolve({ data: detail })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

function roleOptions(): string[] {
  // The Role select is the first combobox on the page; no groups are
  // available to add, so the "Add to group" select is absent.
  return Array.from(screen.getAllByRole('combobox')[0].querySelectorAll('option')).map((o) => o.value)
}

beforeEach(() => {
  vi.restoreAllMocks()
  useAuthStore.setState({
    user: {
      id: 'u-0', email: 'admin@example.com', display_name: 'An Admin', role: 'admin',
      mfa_enabled: true, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
  mockDetail(SERVICE)
})

describe('a service user on the user page', () => {
  it('is badged and points to the OAuth clients page', async () => {
    renderWithQuery(<UserDetailPage />)
    await screen.findByRole('heading', { name: 'JIRA sync' })
    expect(screen.getByText('Service')).toBeTruthy()
    // The nav also has an OAuth clients link; this is the one in the page's intro.
    const link = screen.getByText('OAuth clients', { selector: 'p a' })
    expect(link.getAttribute('href')).toBe('/admin/oauth-clients')
  })

  it('offers no password, MFA or passkey controls', async () => {
    renderWithQuery(<UserDetailPage />)
    await screen.findByRole('heading', { name: 'JIRA sync' })
    expect(screen.queryByText('Reset password')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Reset MFA' })).toBeNull()
    expect(screen.queryByText('MFA')).toBeNull()
    expect(screen.queryByText('Not enrolled')).toBeNull()
    expect(screen.queryByText('Enrolled')).toBeNull()
  })

  it('hides the email field, which the system made', async () => {
    renderWithQuery(<UserDetailPage />)
    await screen.findByRole('heading', { name: 'JIRA sync' })
    expect(screen.queryByDisplayValue(SERVICE.email)).toBeNull()
    expect(screen.getAllByRole('textbox')).toHaveLength(1) // display name only
  })

  it('can be set to staff or user, never admin', async () => {
    renderWithQuery(<UserDetailPage />)
    await screen.findByRole('heading', { name: 'JIRA sync' })
    expect(roleOptions()).toEqual(['staff', 'user'])
  })

  it('saves a rename without sending its system-made email address', async () => {
    const user = userEvent.setup()
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: {} })
    renderWithQuery(<UserDetailPage />)
    await screen.findByRole('heading', { name: 'JIRA sync' })
    const name = screen.getAllByRole('textbox')[0]
    await user.clear(name)
    await user.type(name, 'JIRA two')
    await user.click(screen.getByRole('button', { name: 'Save profile' }))
    await waitFor(() => expect(patch).toHaveBeenCalled())
    expect(patch).toHaveBeenCalledWith('/admin/users/u-2', { display_name: 'JIRA two', role: 'staff' })
  })

  it('can still be disabled', async () => {
    renderWithQuery(<UserDetailPage />)
    await screen.findByRole('heading', { name: 'JIRA sync' })
    expect(screen.getByRole('button', { name: 'Disable' })).toBeTruthy()
    expect(
      screen.getByText('Stops the OAuth client: its requests are refused until this is enabled again.'),
    ).toBeTruthy()
  })

  it('says enabling restores the client when it is disabled', async () => {
    mockDetail({ ...SERVICE, disabled: true })
    renderWithQuery(<UserDetailPage />)
    await screen.findByRole('heading', { name: 'JIRA sync' })
    expect(screen.getByText('Let the OAuth client work again.')).toBeTruthy()
  })

  it('refreshes the security warnings when a group is added, so the banner clears', async () => {
    const user = userEvent.setup()
    vi.spyOn(api, 'get').mockImplementation(((url: string) => {
      if (url === '/admin/users/u-2') return Promise.resolve({ data: SERVICE })
      if (url === '/admin/groups') return Promise.resolve({ data: [{ id: 'g-1', name: 'Service Desk' }] })
      return Promise.resolve({ data: [] })
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    }) as any)
    vi.spyOn(api, 'post').mockResolvedValue({ data: {} })
    const { queryClient } = renderWithQuery(<UserDetailPage />)
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')
    await screen.findByRole('heading', { name: 'JIRA sync' })

    await user.selectOptions(await screen.findByRole('option', { name: 'Service Desk' }).then((o) => o.parentElement as HTMLSelectElement), 'g-1')
    await user.click(screen.getByRole('button', { name: 'Add' }))

    await waitFor(() =>
      expect(invalidate).toHaveBeenCalledWith({ queryKey: ['security-warnings'] }),
    )
  })

  it('shows the 409 message on delete, with a link to the OAuth clients page', async () => {
    const user = userEvent.setup()
    vi.spyOn(api, 'delete').mockRejectedValue(conflict())
    renderWithQuery(<UserDetailPage />)
    await screen.findByRole('heading', { name: 'JIRA sync' })

    await user.click(screen.getByRole('button', { name: 'Delete user' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: 'Delete user' }))

    expect(await screen.findByText(DELETE_MESSAGE)).toBeTruthy()
    const alert = screen.getByText(DELETE_MESSAGE).closest('[role="alert"]') as HTMLElement
    expect(within(alert).getByRole('link', { name: /OAuth clients/i }).getAttribute('href')).toBe(
      '/admin/oauth-clients',
    )
  })
})

describe('an ordinary user on the user page', () => {
  it('still gets the password, MFA and admin role controls', async () => {
    mockDetail({ ...SERVICE, auth_type: 'local', service_account: false, has_password: true, mfa_enabled: true })
    renderWithQuery(<UserDetailPage />)
    await screen.findByRole('heading', { name: 'JIRA sync' })
    expect(screen.getByText('Reset password')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Reset MFA' })).toBeTruthy()
    expect(screen.queryByText('Service')).toBeNull()
    expect(roleOptions()).toContain('admin')
  })
})
