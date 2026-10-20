import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError, type InternalAxiosRequestConfig } from 'axios'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #555: a mistyped CURRENT password answered 401, which the API client reads as
// an expired session, so the person was signed out. The server now answers 400
// invalid_current_password. This drives the page through the REAL client (only
// the network is replaced) so the interceptor is in the path.

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
  useRouterState: () => ({ location: { pathname: '/account' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))
vi.mock('@/api/passkeys', async (orig) => {
  const actual = await orig<typeof import('@/api/passkeys')>()
  return { ...actual, listPasskeys: vi.fn().mockResolvedValue([]), browserSupportsPasskeys: vi.fn(() => true) }
})
vi.mock('@/hooks/useSiteBranding', () => ({ useSiteBranding: () => ({ name: 'Help Desk', logoURL: null }) }))

import { AccountPage } from './AccountPage'

const original = window.location
const originalAdapter = api.defaults.adapter

// What the server answers to the password change; each test sets it.
let refusal = { status: 400, code: 'invalid_current_password' }

beforeEach(() => {
  refusal = { status: 400, code: 'invalid_current_password' }
  Object.defineProperty(window, 'location', { configurable: true, writable: true, value: { pathname: '/account', href: '' } })
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'sam@example.com', display_name: 'Sam', role: 'staff', mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
  api.defaults.adapter = async (config: InternalAxiosRequestConfig) => {
    if (config.url === '/me/password') {
      throw new AxiosError('Request failed', 'ERR_BAD_REQUEST', config, {}, {
        status: refusal.status, statusText: 'Refused', headers: {}, config,
        data: { error: { code: refusal.code, message: 'current password is incorrect' } },
      })
    }
    return { data: [], status: 200, statusText: 'OK', headers: {}, config }
  }
})

afterEach(() => {
  api.defaults.adapter = originalAdapter
  Object.defineProperty(window, 'location', { value: original, writable: true, configurable: true })
})

async function submitWrongCurrentPassword() {
  const user = userEvent.setup()
  renderWithQuery(<AccountPage />)
  await user.type(await screen.findByLabelText('Current password'), 'not-it')
  await user.type(screen.getByLabelText('New password'), 'a-much-better-password')
  await user.type(screen.getByLabelText('Confirm new password'), 'a-much-better-password')
  await user.click(screen.getByRole('button', { name: 'Change password' }))
}

describe('a wrong current password on the account page', () => {
  // Proves the "stays signed in" check can fail: the answer the server used to
  // give is a 401, and the client sends the person to /login on it.
  it('would have been sent to /login by the old 401', async () => {
    refusal = { status: 401, code: 'invalid_credentials' }
    await submitWrongCurrentPassword()
    await waitFor(() => expect(window.location.href).toBe('/login'))
  })

  it('shows the error inline and keeps the person signed in', async () => {
    await submitWrongCurrentPassword()
    expect(await screen.findByText('current password is incorrect')).toBeTruthy()
    expect(window.location.href).toBe('') // not sent to /login
    expect(useAuthStore.getState().user?.email).toBe('sam@example.com')
  })
})
