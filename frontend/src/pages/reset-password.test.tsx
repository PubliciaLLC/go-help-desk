import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'

// #451: the page a reset link opens. It never signs anyone in: on success it
// says the password is changed and sends the person to the sign-in page, where
// the new password and any second factor are asked for.

const navigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigate,
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))
vi.mock('@/api/auth', () => ({ resetPassword: vi.fn(), getMe: vi.fn() }))

import { ResetPasswordPage } from './ResetPasswordPage'
import { resetPassword, getMe } from '@/api/auth'
import { useAuthStore } from '@/store/auth'

function apiError(code: string, message = code) {
  return { isAxiosError: true, response: { status: 400, data: { error: { code, message } } } }
}

beforeEach(() => {
  vi.mocked(resetPassword).mockResolvedValue(undefined)
  window.history.replaceState({}, '', '/reset-password?token=tok-123')
})
afterEach(() => {
  vi.clearAllMocks()
  window.history.replaceState({}, '', '/')
})

async function choose(password: string, confirm = password) {
  const user = userEvent.setup()
  renderWithQuery(<ResetPasswordPage />)
  await user.type(await screen.findByLabelText('New password'), password)
  await user.type(screen.getByLabelText('Confirm new password'), confirm)
  await user.click(screen.getByRole('button', { name: /change password/i }))
}

describe('the reset password page', () => {
  it('waits for a password rather than using the link on load', async () => {
    renderWithQuery(<ResetPasswordPage />)
    expect(await screen.findByLabelText('New password')).toBeTruthy()
    expect(resetPassword).not.toHaveBeenCalled()
  })

  it('sends the token from the link with the password', async () => {
    await choose('a-brand-new-password')
    await waitFor(() => expect(resetPassword).toHaveBeenCalledWith('tok-123', 'a-brand-new-password'))
  })

  it('refuses two passwords that differ, without using the link', async () => {
    await choose('a-brand-new-password', 'something-else-entirely')
    expect((await screen.findByRole('alert')).textContent).toMatch(/do not match/i)
    expect(resetPassword).not.toHaveBeenCalled()
  })

  it('says the password is changed and goes to the sign-in page, signed in as nobody', async () => {
    await choose('a-brand-new-password')

    expect((await screen.findByRole('status')).textContent).toMatch(/password has been changed/i)
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: '/login' }), { timeout: 4000 })
    expect(getMe).not.toHaveBeenCalled()
    expect(useAuthStore.getState().user).toBeNull()
  })

  it('keeps the form for a password the server refuses, because the link is still good', async () => {
    vi.mocked(resetPassword).mockRejectedValue(apiError('password_too_short', 'password must be at least 8 characters'))
    await choose('short')

    expect((await screen.findByRole('alert')).textContent).toMatch(/at least 8 characters/)
    expect(screen.getByLabelText('New password')).toBeTruthy()
    expect(navigate).not.toHaveBeenCalled()
  })

  it('replaces the form with a way to ask again when the link is dead', async () => {
    vi.mocked(resetPassword).mockRejectedValue(apiError('token_invalid'))
    await choose('a-brand-new-password')

    expect((await screen.findByRole('alert')).textContent).toMatch(/invalid, has expired or has already been used/i)
    expect(screen.queryByLabelText('New password')).toBeNull()
    expect(screen.getByRole('link', { name: /request a new link/i }).getAttribute('href')).toBe('/forgot-password')
    expect(navigate).not.toHaveBeenCalled()
  })

  it('does not show a form at all when the address has no token', async () => {
    window.history.replaceState({}, '', '/reset-password')
    renderWithQuery(<ResetPasswordPage />)

    expect(await screen.findByRole('alert')).toBeTruthy()
    expect(screen.queryByLabelText('New password')).toBeNull()
    expect(screen.getByRole('link', { name: /request a new link/i })).toBeTruthy()
  })

  it('treats a fault as something to retry, not as a verdict on the link', async () => {
    vi.mocked(resetPassword).mockRejectedValue({ isAxiosError: true, response: { status: 500, data: {} } })
    await choose('a-brand-new-password')

    expect((await screen.findByRole('alert')).textContent).toMatch(/something went wrong/i)
    expect(screen.getByLabelText('New password')).toBeTruthy()
  })
})
