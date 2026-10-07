import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// "Reset MFA" cleared the TOTP columns only, and the page showed the button
// only when the TOTP flag was set (#307). A passkey-only account therefore had
// no button at all: the person lost their key and the administrator had
// nothing to press. The button now follows "holds any second factor", and says
// what it clears.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/users/u-2' } }),
  useNavigate: () => vi.fn(),
  useParams: () => ({ id: 'u-2' }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

import { UserDetailPage } from './UserDetailPage'

function user(overrides: Record<string, unknown>) {
  return {
    id: 'u-2',
    email: 'someone@example.com',
    display_name: 'Someone',
    role: 'staff',
    disabled: false,
    mfa_enabled: false,
    has_password: true,
    auth_type: 'local',
    groups: [],
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

function mockApi(detail: Record<string, unknown>) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/users/u-2') return Promise.resolve({ data: detail })
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

describe('Reset MFA on the admin user page', () => {
  it('is offered for an account whose only factor is a passkey', async () => {
    mockApi(user({ mfa_enabled: false, passkey_count: 1 }))
    renderWithQuery(<UserDetailPage />)

    expect(await screen.findByRole('button', { name: 'Reset MFA' })).toBeTruthy()
    expect(screen.getByText(/every passkey/)).toBeTruthy()
  })

  it('is offered for an authenticator-only account, as before', async () => {
    mockApi(user({ mfa_enabled: true, passkey_count: 0 }))
    renderWithQuery(<UserDetailPage />)

    expect(await screen.findByRole('button', { name: 'Reset MFA' })).toBeTruthy()
  })

  it('is not offered when there is nothing to reset', async () => {
    mockApi(user({ mfa_enabled: false, passkey_count: 0 }))
    renderWithQuery(<UserDetailPage />)

    await screen.findByText('Someone')
    expect(screen.queryByRole('button', { name: 'Reset MFA' })).toBeNull()
  })
})
