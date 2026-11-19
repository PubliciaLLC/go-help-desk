import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import type { LoginResponse } from '@/api/auth'

// #616: a role that must use a passkey. TOTP proves a factor for it but does
// not pass MFA, so the sign-in goes on to register a key (or use one).
// The server enforces all of it; these tests pin which screen the page shows.

const navigate = vi.fn()

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigate,
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

vi.mock('@/api/auth', () => ({
  login: vi.fn(),
  verifyMFA: vi.fn(),
  getMe: vi.fn(),
  changePassword: vi.fn(),
  enrollMFAStart: vi.fn(),
  enrollMFAConfirm: vi.fn(),
  getSignupStatus: vi.fn().mockResolvedValue({ enabled: false, open_registration: false, saml_enabled: false }),
  getAuthProviders: vi.fn().mockResolvedValue({ providers: [{ name: 'password', enabled: true }] }),
}))
vi.mock('@/api/admin', () => ({ getSiteConfig: vi.fn().mockResolvedValue({}) }))
vi.mock('@/api/passkeys', () => ({
  signInWithPasskey: vi.fn(),
  addPasskey: vi.fn(),
  browserSupportsPasskeys: () => true,
  wasCancelled: () => false,
}))
vi.mock('@/hooks/useSiteBranding', () => ({ useSiteBranding: () => ({ name: 'Help Desk', logoURL: null }) }))

import { LoginPage } from './LoginPage'
import { login, verifyMFA, getMe, enrollMFAStart } from '@/api/auth'
import { addPasskey, signInWithPasskey } from '@/api/passkeys'

const USER = {
  id: 'u-1',
  email: 'sam@example.com',
  display_name: 'Sam',
  role: 'staff' as const,
  mfa_enabled: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
}

function answer(over: Partial<LoginResponse>): LoginResponse {
  return { user: USER, mfa_needed: false, passkey_needed: false, mfa_enrollment_needed: false, ...over }
}

function renderAt(search = '') {
  window.history.replaceState({}, '', '/login' + search)
  return renderWithQuery(<LoginPage />)
}

async function signIn() {
  const user = userEvent.setup()
  await user.type(await screen.findByLabelText(/email/i), 'sam@example.com')
  await user.type(screen.getByLabelText(/password/i), 'password')
  await user.click(screen.getByRole('button', { name: /sign in/i }))
  return user
}

beforeEach(() => {
  vi.mocked(getMe).mockResolvedValue(USER)
  vi.mocked(verifyMFA).mockResolvedValue(undefined)
  vi.mocked(addPasskey).mockResolvedValue(undefined)
})
afterEach(() => {
  vi.clearAllMocks()
  window.history.replaceState({}, '', '/')
})

describe('signing in with a password in a passkey-required role', () => {
  it('registers a key when the account has none, then goes to the dashboard', async () => {
    vi.mocked(login).mockResolvedValue(answer({ passkey_enrollment_needed: true }))
    renderAt()
    const user = await signIn()

    await screen.findByRole('heading', { name: /register a passkey/i })
    expect(enrollMFAStart).not.toHaveBeenCalled()
    expect(navigate).not.toHaveBeenCalled()

    await user.type(screen.getByLabelText(/name for this passkey/i), 'Work laptop')
    await user.click(screen.getByRole('button', { name: /register a passkey/i }))
    await waitFor(() => expect(addPasskey).toHaveBeenCalledWith('Work laptop'))
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: '/dashboard' }))
  })

  it('verifies the authenticator code first, then registers a key', async () => {
    vi.mocked(login).mockResolvedValue(answer({ mfa_needed: true, passkey_enrollment_needed: true }))
    renderAt()
    const user = await signIn()

    await user.type(await screen.findByLabelText(/verification code/i), '123456')
    await user.click(screen.getByRole('button', { name: /verify/i }))

    await screen.findByRole('heading', { name: /register a passkey/i })
    expect(verifyMFA).toHaveBeenCalledWith('123456')
    expect(navigate).not.toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: /register a passkey/i }))
    await waitFor(() => expect(addPasskey).toHaveBeenCalledWith('My passkey'))
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: '/dashboard' }))
  })

  it('still goes straight to the dashboard after a plain code when no key is owed', async () => {
    vi.mocked(login).mockResolvedValue(answer({ mfa_needed: true }))
    renderAt()
    const user = await signIn()

    await user.type(await screen.findByLabelText(/verification code/i), '123456')
    await user.click(screen.getByRole('button', { name: /verify/i }))
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: '/dashboard' }))
    expect(addPasskey).not.toHaveBeenCalled()
  })
})

describe('the step single sign-on asks for', () => {
  it('shows the passkey sign-in for ?sso_step=passkey', async () => {
    renderAt('?sso_step=passkey')
    await screen.findByRole('heading', { name: /confirm it is you/i })
    const user = userEvent.setup()
    vi.mocked(signInWithPasskey).mockResolvedValue(undefined)
    await user.click(screen.getByRole('button', { name: /continue with a passkey/i }))
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: '/dashboard' }))
  })

  it('shows passkey registration for ?sso_step=passkey_enroll', async () => {
    renderAt('?sso_step=passkey_enroll')
    await screen.findByRole('heading', { name: /register a passkey/i })
    expect(screen.queryByLabelText(/^email/i)).toBeNull()
  })

  it('shows the code step, then registration, for ?sso_step=verify_then_passkey_enroll', async () => {
    renderAt('?sso_step=verify_then_passkey_enroll')
    const user = userEvent.setup()
    await user.type(await screen.findByLabelText(/verification code/i), '123456')
    await user.click(screen.getByRole('button', { name: /verify/i }))
    await screen.findByRole('heading', { name: /register a passkey/i })
    expect(navigate).not.toHaveBeenCalled()
  })

  it.each(['', '?sso_step=nonsense', '?sso_step=constructor', '?sso_step=__proto__', '?sso_step=PASSKEY'])(
    'ignores an unknown value (%s) and shows the ordinary form',
    async (search) => {
      renderAt(search)
      expect(await screen.findByLabelText(/^email/i)).toBeTruthy()
      expect(screen.queryByRole('heading', { name: /register a passkey/i })).toBeNull()
    },
  )
})
