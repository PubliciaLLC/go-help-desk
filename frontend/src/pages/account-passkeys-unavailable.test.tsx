import { describe, expect, it, vi, afterEach, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import { AxiosError } from 'axios'
import { renderWithQuery } from '@/test/render'
import { useAuthStore } from '@/store/auth'

// #494: BASE_URL that cannot be a passkey domain (an IP address, a
// single-label host) turns passkeys off on the server. The account page must
// say so rather than offer to enrol a key that cannot work.

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
  useRouterState: () => ({ location: { pathname: '/account' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

vi.mock('@/api/passkeys', async (orig) => {
  const actual = await orig<typeof import('@/api/passkeys')>()
  return {
    ...actual,
    listPasskeys: vi.fn(),
    addPasskey: vi.fn(),
    removePasskey: vi.fn(),
    browserSupportsPasskeys: vi.fn(() => true),
  }
})

vi.mock('@/api/auth', () => ({
  changePassword: vi.fn(),
  enrollMFAStart: vi.fn(),
  enrollMFAConfirm: vi.fn(),
  logout: vi.fn(),
}))

vi.mock('@/hooks/useSiteBranding', () => ({ useSiteBranding: () => ({ name: 'Help Desk', logoURL: null }) }))

import { AccountPage } from './AccountPage'
import { listPasskeys } from '@/api/passkeys'

function unavailable() {
  return new AxiosError('Service Unavailable', 'ERR_BAD_RESPONSE', undefined, undefined, {
    status: 503,
    data: { error: { code: 'passkeys_unavailable', message: 'passkeys are not available on this instance' } },
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  } as any)
}

beforeEach(() => {
  useAuthStore.setState({
    user: {
      id: 'u-1',
      email: 'sam@example.com',
      display_name: 'Sam',
      role: 'staff',
      mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })
})

afterEach(() => vi.clearAllMocks())

describe('the passkey section when the server has passkeys off', () => {
  it('explains it and offers no way to add a key', async () => {
    vi.mocked(listPasskeys).mockRejectedValue(unavailable())
    renderWithQuery(<AccountPage />)

    expect(await screen.findByText(/Passkeys are not available on this instance/)).toBeTruthy()
    expect(screen.queryByRole('button', { name: /Add a passkey/ })).toBeNull()
    expect(screen.queryByLabelText('Name this key')).toBeNull()
  })

  it('does not mistake some other failure for it', async () => {
    vi.mocked(listPasskeys).mockRejectedValue(new Error('network down'))
    renderWithQuery(<AccountPage />)

    // Still the normal section: a transient fault must not hide enrolment.
    expect(await screen.findByRole('button', { name: /Add a passkey/ })).toBeTruthy()
    expect(screen.queryByText(/Passkeys are not available on this instance/)).toBeNull()
  })
})
