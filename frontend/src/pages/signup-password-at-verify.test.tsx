import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'

// #360: the password is chosen on the verification page, not on the signup
// form. When signup carried it, a second signup for the same address replaced
// it, and the owner of the address — following the newest link — got an
// account with somebody else's password. Only whoever reads the inbox reaches
// the verification page, so that is where the password is chosen.

const navigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigate,
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))
vi.mock('@/api/auth', () => ({ signup: vi.fn(), verifyEmail: vi.fn() }))

import { SignupPage } from './SignupPage'
import { VerifyEmailPage } from './VerifyEmailPage'
import { signup, verifyEmail } from '@/api/auth'

function apiError(code: string, message = code) {
  return { isAxiosError: true, response: { data: { error: { code, message } } } }
}

beforeEach(() => {
  vi.mocked(signup).mockResolvedValue(undefined)
  window.history.replaceState({}, '', '/verify-email?token=tok-123')
})
afterEach(() => vi.clearAllMocks())

describe('the signup form', () => {
  it('asks for no password and sends none', async () => {
    const user = userEvent.setup()
    renderWithQuery(<SignupPage />)
    expect(screen.queryByLabelText(/password/i)).toBeNull()

    await user.type(screen.getByLabelText('Email'), 'alice@example.com')
    await user.type(screen.getByLabelText('Display name'), 'Alice')
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => expect(signup).toHaveBeenCalledWith('alice@example.com', 'Alice'))
  })
})

describe('the verification page', () => {
  async function choose(user: ReturnType<typeof userEvent.setup>, password: string, confirm = password) {
    await user.type(screen.getByLabelText('Password'), password)
    await user.type(screen.getByLabelText('Confirm password'), confirm)
    await user.click(screen.getByRole('button', { name: /create account/i }))
  }

  it('waits for a password rather than verifying on load', () => {
    renderWithQuery(<VerifyEmailPage />)
    expect(screen.getByLabelText('Password')).toBeTruthy()
    expect(verifyEmail).not.toHaveBeenCalled()
  })

  it('sends the token with the chosen password, then signs in', async () => {
    vi.mocked(verifyEmail).mockResolvedValue({ user: { id: 'u1' } } as never)
    const user = userEvent.setup()
    renderWithQuery(<VerifyEmailPage />)
    await choose(user, 'correct-horse-battery')

    await waitFor(() => expect(verifyEmail).toHaveBeenCalledWith('tok-123', 'correct-horse-battery'))
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: '/dashboard' }))
  })

  it('refuses a mismatched password and asks the server nothing', async () => {
    const user = userEvent.setup()
    renderWithQuery(<VerifyEmailPage />)
    await choose(user, 'correct-horse-battery', 'correct-horse-batterx')

    expect(await screen.findByRole('alert')).toBeTruthy()
    expect(verifyEmail).not.toHaveBeenCalled()
  })

  it('keeps the form after a too-short password, so the link can still be used', async () => {
    vi.mocked(verifyEmail).mockRejectedValue(apiError('password_too_short', 'password must be at least 8 characters'))
    const user = userEvent.setup()
    renderWithQuery(<VerifyEmailPage />)
    await choose(user, 'shortpw')

    expect((await screen.findByRole('alert')).textContent).toMatch(/at least 8 characters/i)
    expect(screen.getByLabelText('Password')).toBeTruthy()
  })

  it('says an expired link has expired', async () => {
    vi.mocked(verifyEmail).mockRejectedValue(apiError('token_expired'))
    const user = userEvent.setup()
    renderWithQuery(<VerifyEmailPage />)
    await choose(user, 'correct-horse-battery')

    expect((await screen.findByRole('alert')).textContent).toMatch(/expired/i)
  })
})
