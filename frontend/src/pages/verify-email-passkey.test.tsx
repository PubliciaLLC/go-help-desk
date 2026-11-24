import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'

// #616: a new account in a role that must use a passkey has no factor yet, so
// verifying the address answers passkey_enrollment_needed and the page asks for
// a key, not an authenticator code.

const navigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigate,
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))
vi.mock('@/api/auth', () => ({
  verifyEmail: vi.fn(),
  lookupVerification: vi.fn(),
  getMe: vi.fn(),
  enrollMFAStart: vi.fn(),
  enrollMFAConfirm: vi.fn(),
}))
vi.mock('@/api/passkeys', () => ({
  addPasskey: vi.fn(),
  browserSupportsPasskeys: () => true,
  wasCancelled: () => false,
}))

import { VerifyEmailPage } from './VerifyEmailPage'
import { verifyEmail, lookupVerification, getMe, enrollMFAStart } from '@/api/auth'
import { addPasskey } from '@/api/passkeys'

beforeEach(() => {
  vi.mocked(lookupVerification).mockResolvedValue({ email: 'dora@example.com' })
  vi.mocked(getMe).mockResolvedValue({ id: 'u1' } as never)
  vi.mocked(addPasskey).mockResolvedValue(undefined)
  window.history.replaceState({}, '', '/verify-email?token=tok-123')
})
afterEach(() => vi.clearAllMocks())

describe('verifying a signup in a passkey-required role', () => {
  it('asks for a passkey, and goes to the dashboard only once it is registered', async () => {
    vi.mocked(verifyEmail).mockResolvedValue({
      user: { id: 'u1' }, mfa_needed: false, passkey_needed: false,
      mfa_enrollment_needed: false, passkey_enrollment_needed: true,
    } as never)
    const user = userEvent.setup()
    renderWithQuery(<VerifyEmailPage />)

    await user.type(await screen.findByLabelText('Display name'), 'Dora')
    await user.type(screen.getByLabelText('Password'), 'correct-horse-battery')
    await user.type(screen.getByLabelText('Confirm password'), 'correct-horse-battery')
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await screen.findByRole('heading', { name: /register a passkey/i })
    expect(enrollMFAStart).not.toHaveBeenCalled()
    expect(navigate).not.toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: /register a passkey/i }))
    await waitFor(() => expect(addPasskey).toHaveBeenCalled())
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: '/dashboard' }))
    expect(getMe).toHaveBeenCalled()
  })
})
