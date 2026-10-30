import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import { AxiosError, AxiosHeaders } from 'axios'

// The account actions on an admin's user page, when the server says no.
//
// Disable, Reset MFA, Remove from group and Delete had no error handling at
// all. The refusal that matters is the last-administrator guard — the server
// answers 409 with "this is the only administrator, so it cannot be disabled,
// demoted or deleted" — and every one of those buttons dropped it: the label
// went to "…", came back, and the account was unchanged with nothing said.
//
// The role change in the same card showed its error the whole time, so the
// identical refusal was explained on one control and swallowed on the one
// below it.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/users/u-2' } }),
  useNavigate: () => vi.fn(),
  useParams: () => ({ id: 'u-2' }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

import { UserDetailPage } from './UserDetailPage'

const LAST_ADMIN_MESSAGE =
  'this is the only administrator, so it cannot be disabled, demoted or deleted'

const USER = {
  id: 'u-2',
  email: 'only.admin@example.com',
  display_name: 'Only Admin',
  role: 'admin',
  disabled: false,
  mfa_enabled: true,
  has_password: true,
  auth_type: 'local',
  groups: [{ id: 'g-1', name: 'Service Desk' }],
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
}

function refusal(message: string, status = 409) {
  const headers = new AxiosHeaders()
  return new AxiosError('Request failed', 'ERR_BAD_REQUEST', { headers } as never, null, {
    status,
    statusText: 'Conflict',
    headers,
    config: { headers } as never,
    data: { error: { code: 'last_admin', message } },
  } as never)
}

function mockApi() {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === `/admin/users/u-2`) return Promise.resolve({ data: USER })
    if (url === '/admin/groups') return Promise.resolve({ data: [{ id: 'g-1', name: 'Service Desk' }] })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

beforeEach(() => {
  vi.restoreAllMocks()
  useAuthStore.setState({
    user: {
      id: 'u-1',
      email: 'admin@example.com',
      display_name: 'An Admin',
      role: 'admin',
      mfa_enabled: true,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })
})

describe('when the server refuses an account action', () => {
  it('says why the account could not be disabled', async () => {
    mockApi()
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    vi.spyOn(api, 'patch').mockRejectedValue(refusal(LAST_ADMIN_MESSAGE) as any)
    renderWithQuery(<UserDetailPage />)

    const button = await screen.findByRole('button', { name: 'Disable' })
    await userEvent.click(button)

    await waitFor(() => {
      const alerts = Array.from(document.querySelectorAll('[role="alert"]')).map(
        (n) => n.textContent ?? '',
      )
      expect(
        alerts.some((t) => t.includes(LAST_ADMIN_MESSAGE)),
        `nothing on the page said why: ${JSON.stringify(alerts)}`,
      ).toBe(true)
    })
  })

  it('says why MFA could not be reset', async () => {
    mockApi()
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    vi.spyOn(api, 'patch').mockRejectedValue(refusal('that account is federated', 400) as any)
    renderWithQuery(<UserDetailPage />)

    const button = await screen.findByRole('button', { name: 'Reset MFA' })
    await userEvent.click(button)

    await waitFor(() => {
      const alerts = Array.from(document.querySelectorAll('[role="alert"]')).map(
        (n) => n.textContent ?? '',
      )
      expect(alerts.some((t) => t.includes('that account is federated'))).toBe(true)
    })
  })

  it('says why they could not be removed from a group', async () => {
    mockApi()
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    vi.spyOn(api, 'delete').mockRejectedValue(refusal('the group has open tickets', 409) as any)
    renderWithQuery(<UserDetailPage />)

    const remove = await screen.findByRole('button', { name: 'Remove' })
    await userEvent.click(remove)

    await waitFor(() => {
      const alerts = Array.from(document.querySelectorAll('[role="alert"]')).map(
        (n) => n.textContent ?? '',
      )
      expect(alerts.some((t) => t.includes('the group has open tickets'))).toBe(true)
    })
  })
})
