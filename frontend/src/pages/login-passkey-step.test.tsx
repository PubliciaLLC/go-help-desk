import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import type { LoginResponse } from '@/api/auth'

// Login's fourth answer. An account holding passkeys and no authenticator app
// has a second factor that is a key rather than a code, so the code screen is
// the wrong screen to show it — and before `passkey_needed` existed the
// server had no way to say which.

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
  enrollMFAStart: vi.fn(),
  enrollMFAConfirm: vi.fn(),
  getSignupStatus: vi.fn().mockResolvedValue({ enabled: false, open_registration: false, saml_enabled: false }),
  getAuthProviders: vi.fn().mockResolvedValue({ providers: [{ name: 'password', enabled: true }] }),
}))
vi.mock('@/api/admin', () => ({ getSiteConfig: vi.fn().mockResolvedValue({}) }))
vi.mock('@/api/passkeys', () => ({
  signInWithPasskey: vi.fn(),
  wasCancelled: (e: unknown) => e instanceof DOMException && (e.name === 'NotAllowedError' || e.name === 'AbortError'),
}))
vi.mock('@/hooks/useSiteBranding', () => ({ useSiteBranding: () => ({ name: 'Help Desk', logoURL: null }) }))

import { LoginPage } from './LoginPage'
import { login, getMe, enrollMFAStart, enrollMFAConfirm } from '@/api/auth'
import { signInWithPasskey } from '@/api/passkeys'

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

async function signIn() {
  const user = userEvent.setup()
  renderWithQuery(<LoginPage />)
  await user.type(await screen.findByLabelText(/email/i), 'sam@example.com')
  await user.type(screen.getByLabelText(/password/i), 'hunter2')
  await user.click(screen.getByRole('button', { name: /sign in/i }))
  return user
}

beforeEach(() => {
  vi.mocked(getMe).mockResolvedValue(USER)
  // The enrol step kicks this off from an effect the moment it renders, and
  // a mock returning undefined takes the whole page down with it.
  vi.mocked(enrollMFAStart).mockResolvedValue({
    secret: 'JBSWY3DPEHPK3PXP',
    qr_url: 'otpauth://x',
    qr_data_url: 'data:image/png;base64,AAAA',
  })
})
afterEach(() => vi.clearAllMocks())

describe('a passkey-only account signing in', () => {
  it('is asked for the key, not for a six-digit code', async () => {
    vi.mocked(login).mockResolvedValue(answer({ passkey_needed: true }))
    await signIn()

    expect(await screen.findByRole('button', { name: /Continue with a passkey/ })).toBeTruthy()
    // The code field would be unanswerable: there is no authenticator app on
    // this account to read a code from.
    expect(screen.queryByLabelText(/Verification code/)).toBeNull()
    expect(navigate).not.toHaveBeenCalled()
  })

  it('completes the sign-in once the key is proved', async () => {
    vi.mocked(login).mockResolvedValue(answer({ passkey_needed: true }))
    vi.mocked(signInWithPasskey).mockResolvedValue(undefined)
    const user = await signIn()

    await user.click(await screen.findByRole('button', { name: /Continue with a passkey/ }))
    await waitFor(() => expect(signInWithPasskey).toHaveBeenCalled())
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: '/dashboard' }))
  })

  it('leaves them on the screen with the button when they dismiss the prompt', async () => {
    vi.mocked(login).mockResolvedValue(answer({ passkey_needed: true }))
    vi.mocked(signInWithPasskey).mockRejectedValue(new DOMException('', 'NotAllowedError'))
    const user = await signIn()

    await user.click(await screen.findByRole('button', { name: /Continue with a passkey/ }))
    await waitFor(() => expect(signInWithPasskey).toHaveBeenCalled())
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.getByRole('button', { name: /Continue with a passkey/ })).toBeTruthy()
    expect(navigate).not.toHaveBeenCalled()
  })

  it('reports a key that did not verify', async () => {
    vi.mocked(login).mockResolvedValue(answer({ passkey_needed: true }))
    vi.mocked(signInWithPasskey).mockRejectedValue(new DOMException('', 'SecurityError'))
    const user = await signIn()

    await user.click(await screen.findByRole('button', { name: /Continue with a passkey/ }))
    expect(await screen.findByRole('alert')).toBeTruthy()
    expect(navigate).not.toHaveBeenCalled()
  })
})

describe('the other three answers are unchanged', () => {
  it('a TOTP account still gets the code screen', async () => {
    vi.mocked(login).mockResolvedValue(answer({ mfa_needed: true }))
    await signIn()
    expect(await screen.findByLabelText(/Verification code/)).toBeTruthy()
    expect(screen.queryByRole('button', { name: /Continue with a passkey/ })).toBeNull()
  })

  it('an account with no factor at all goes straight through', async () => {
    vi.mocked(login).mockResolvedValue(answer({}))
    await signIn()
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: '/dashboard' }))
  })

  it('enrolment still wins over everything else', async () => {
    // Belt and braces: the server should never send both, but if it did, the
    // screen that lets someone get a factor is the one to show.
    vi.mocked(login).mockResolvedValue(answer({ mfa_enrollment_needed: true, passkey_needed: true }))
    await signIn()
    expect(await screen.findByRole('heading', { name: 'Set up two-factor authentication' })).toBeTruthy()
    expect(screen.queryByRole('button', { name: /Continue with a passkey/ })).toBeNull()
  })
})

// #369 moved this step into MFAEnrollForm. The login page's half of the
// contract is what happens after the code is confirmed: sign in and go on.
describe('an account that must enrol at login', () => {
  it('signs in and goes to the dashboard once enrolment is confirmed', async () => {
    vi.mocked(login).mockResolvedValue(answer({ mfa_enrollment_needed: true }))
    vi.mocked(enrollMFAConfirm).mockResolvedValue(undefined)
    const user = await signIn()

    await user.type(await screen.findByLabelText('Verification code'), '123456')
    await waitFor(() => expect((screen.getByRole('button', { name: /confirm/i }) as HTMLButtonElement).disabled).toBe(false))
    expect(navigate).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: /confirm/i }))

    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: '/dashboard' }))
    expect(getMe).toHaveBeenCalled()
  })
})
