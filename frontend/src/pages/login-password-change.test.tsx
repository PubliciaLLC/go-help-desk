import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import type { LoginResponse } from '@/api/auth'

// An account unlocked from the server console (#497) signs in with the
// password it was handed and must replace it before anything else. The sign-in
// answers password_change_required; the page shows that step, then carries on
// to wherever the sign-in was going.

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
import { login, getMe, verifyMFA, changePassword, enrollMFAStart } from '@/api/auth'
import { addPasskey, signInWithPasskey } from '@/api/passkeys'

const USER = {
  id: 'u-1',
  email: 'sam@example.com',
  display_name: 'Sam',
  role: 'admin' as const,
  mfa_enabled: false,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
}

function answer(over: Partial<LoginResponse>): LoginResponse {
  return {
    user: USER,
    mfa_needed: false,
    passkey_needed: false,
    mfa_enrollment_needed: false,
    password_change_required: true,
    ...over,
  }
}

async function signIn() {
  const user = userEvent.setup()
  renderWithQuery(<LoginPage />)
  await user.type(await screen.findByLabelText(/email/i), 'sam@example.com')
  await user.type(screen.getByLabelText(/password/i), 'handed-out-password')
  await user.click(screen.getByRole('button', { name: /sign in/i }))
  return user
}

beforeEach(() => {
  vi.mocked(getMe).mockResolvedValue(USER)
  vi.mocked(enrollMFAStart).mockResolvedValue({
    secret: 'JBSWY3DPEHPK3PXP',
    qr_url: 'otpauth://x',
    qr_data_url: 'data:image/png;base64,AAAA',
  })
})
afterEach(() => vi.clearAllMocks())

describe('signing in with a password that must be changed', () => {
  it('asks for a new password, sends the one just typed as the current one, then goes to the dashboard', async () => {
    vi.mocked(login).mockResolvedValue(answer({}))
    vi.mocked(changePassword).mockResolvedValue(undefined)
    const user = await signIn()

    expect(await screen.findByText(/choose a new password/i)).toBeTruthy()
    expect(navigate).not.toHaveBeenCalled()

    await user.type(screen.getByLabelText(/^new password/i), 'a-brand-new-password')
    await user.type(screen.getByLabelText(/confirm/i), 'a-brand-new-password')
    await user.click(screen.getByRole('button', { name: /change password/i }))

    await waitFor(() =>
      expect(changePassword).toHaveBeenCalledWith('handed-out-password', 'a-brand-new-password'),
    )
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: '/dashboard' }))
  })

  it('does not send two different passwords', async () => {
    vi.mocked(login).mockResolvedValue(answer({}))
    const user = await signIn()

    await user.type(await screen.findByLabelText(/^new password/i), 'a-brand-new-password')
    await user.type(screen.getByLabelText(/confirm/i), 'something-else-entirely')
    await user.click(screen.getByRole('button', { name: /change password/i }))

    expect(await screen.findByRole('alert')).toBeTruthy()
    expect(changePassword).not.toHaveBeenCalled()
  })

  it('goes on to two-factor enrolment afterwards when the account owes it', async () => {
    vi.mocked(login).mockResolvedValue(answer({ mfa_enrollment_needed: true }))
    vi.mocked(changePassword).mockResolvedValue(undefined)
    const user = await signIn()

    await user.type(await screen.findByLabelText(/^new password/i), 'a-brand-new-password')
    await user.type(screen.getByLabelText(/confirm/i), 'a-brand-new-password')
    await user.click(screen.getByRole('button', { name: /change password/i }))

    await waitFor(() => expect(enrollMFAStart).toHaveBeenCalled())
    expect(navigate).not.toHaveBeenCalled()
  })
})

// The server decides the order, and for an account that holds a factor it wants
// the factor first: PATCH /me/password answers 403 mfa_required until a code or
// key has been proved (RequireMFAOrPasswordChange). An account that holds no
// factor has nothing to prove, so the new password comes first. The page reads
// the order off the sign-in answer: mfa_needed or passkey_needed means a factor
// is held.
describe('the order of the factor and the forced password change', () => {
  async function newPassword(user: ReturnType<typeof userEvent.setup>) {
    await screen.findByText(/choose a new password/i)
    await user.type(screen.getByLabelText(/^new password/i), 'a-brand-new-password')
    await user.type(screen.getByLabelText(/confirm/i), 'a-brand-new-password')
    await user.click(screen.getByRole('button', { name: /change password/i }))
  }

  it('asks an authenticator holder for the code before the new password', async () => {
    vi.mocked(login).mockResolvedValue(answer({ mfa_needed: true }))
    vi.mocked(verifyMFA).mockResolvedValue(undefined)
    vi.mocked(changePassword).mockResolvedValue(undefined)
    const user = await signIn()

    await user.type(await screen.findByLabelText(/verification code/i), '123456')
    expect(screen.queryByText(/choose a new password/i)).toBeNull()
    expect(changePassword).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: /verify/i }))

    await waitFor(() => expect(verifyMFA).toHaveBeenCalledWith('123456'))
    expect(navigate).not.toHaveBeenCalled()
    await newPassword(user)

    await waitFor(() =>
      expect(changePassword).toHaveBeenCalledWith('handed-out-password', 'a-brand-new-password'),
    )
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: '/dashboard' }))
  })

  it('asks a passkey holder for the key before the new password', async () => {
    vi.mocked(login).mockResolvedValue(answer({ passkey_needed: true }))
    vi.mocked(signInWithPasskey).mockResolvedValue(undefined)
    vi.mocked(changePassword).mockResolvedValue(undefined)
    const user = await signIn()

    await screen.findByRole('heading', { name: /confirm it is you/i })
    expect(screen.queryByText(/choose a new password/i)).toBeNull()
    await user.click(screen.getByRole('button', { name: /continue with a passkey/i }))
    expect(navigate).not.toHaveBeenCalled()
    await newPassword(user)

    await waitFor(() => expect(changePassword).toHaveBeenCalled())
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: '/dashboard' }))
  })

  it('in a passkey-required role: code, then new password, then the key', async () => {
    vi.mocked(login).mockResolvedValue(answer({ mfa_needed: true, passkey_enrollment_needed: true }))
    vi.mocked(verifyMFA).mockResolvedValue(undefined)
    vi.mocked(changePassword).mockResolvedValue(undefined)
    vi.mocked(addPasskey).mockResolvedValue(undefined)
    const user = await signIn()

    await user.type(await screen.findByLabelText(/verification code/i), '123456')
    await user.click(screen.getByRole('button', { name: /verify/i }))
    await newPassword(user)

    await screen.findByRole('heading', { name: /register a passkey/i })
    expect(navigate).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: /register a passkey/i }))
    await waitFor(() => expect(addPasskey).toHaveBeenCalled())
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: '/dashboard' }))
  })

  it('in a passkey-required role with no factor: new password, then the key', async () => {
    vi.mocked(login).mockResolvedValue(answer({ passkey_enrollment_needed: true }))
    vi.mocked(changePassword).mockResolvedValue(undefined)
    vi.mocked(addPasskey).mockResolvedValue(undefined)
    const user = await signIn()

    await newPassword(user)
    await screen.findByRole('heading', { name: /register a passkey/i })
    expect(verifyMFA).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: /register a passkey/i }))
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: '/dashboard' }))
  })

  it('with no factor at all: new password straight away', async () => {
    vi.mocked(login).mockResolvedValue(answer({}))
    vi.mocked(changePassword).mockResolvedValue(undefined)
    const user = await signIn()

    await newPassword(user)
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: '/dashboard' }))
    expect(verifyMFA).not.toHaveBeenCalled()
  })
})
