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
vi.mock('@/api/auth', () => ({
  signup: vi.fn(),
  verifyEmail: vi.fn(),
  lookupVerification: vi.fn(),
  getMe: vi.fn(),
  enrollMFAStart: vi.fn(),
  enrollMFAConfirm: vi.fn(),
}))

import { SignupPage } from './SignupPage'
import { VerifyEmailPage } from './VerifyEmailPage'
import { signup, verifyEmail, lookupVerification, getMe, enrollMFAStart, enrollMFAConfirm } from '@/api/auth'
import { useAuthStore } from '@/store/auth'

function apiError(code: string, message = code) {
  return { isAxiosError: true, response: { data: { error: { code, message } } } }
}

beforeEach(() => {
  vi.mocked(signup).mockResolvedValue(undefined)
  vi.mocked(lookupVerification).mockResolvedValue({ email: 'alice@example.com' })
  window.history.replaceState({}, '', '/verify-email?token=tok-123')
})
afterEach(() => vi.clearAllMocks())

describe('the signup form', () => {
  // #374: and no name either. Whoever submits this form need not own the
  // address, so nothing they type here may reach the account.
  it('asks for the address only, and sends nothing else', async () => {
    const user = userEvent.setup()
    renderWithQuery(<SignupPage />)
    expect(screen.queryByLabelText(/password/i)).toBeNull()
    expect(screen.queryByLabelText(/name/i)).toBeNull()

    await user.type(screen.getByLabelText('Email'), 'alice@example.com')
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => expect(signup).toHaveBeenCalledWith('alice@example.com'))
  })
})

describe('the verification page', () => {
  async function choose(user: ReturnType<typeof userEvent.setup>, password: string, confirm = password, name = 'Alice') {
    if (name) await user.type(await screen.findByLabelText('Display name'), name)
    await user.type(await screen.findByLabelText('Password'), password)
    await user.type(screen.getByLabelText('Confirm password'), confirm)
    await user.click(screen.getByRole('button', { name: /create account/i }))
  }

  it('waits for a password rather than verifying on load', async () => {
    renderWithQuery(<VerifyEmailPage />)
    expect(await screen.findByLabelText('Password')).toBeTruthy()
    expect(verifyEmail).not.toHaveBeenCalled()
  })

  // #370: the page says which address this is, and hands it to a password
  // manager, so the saved login is not missing its username. Not the display
  // name: whoever signed up first chose it, and an attacker who signs up a
  // victim's address could put any words they like on this page.
  it('shows the address the link is for, and not the name', async () => {
    renderWithQuery(<VerifyEmailPage />)
    const email = (await screen.findByLabelText('Email')) as HTMLInputElement
    expect(lookupVerification).toHaveBeenCalledWith('tok-123')
    expect(email.value).toBe('alice@example.com')
    expect(email.readOnly).toBe(true)
    expect(email.getAttribute('autocomplete')).toBe('username')
  })

  it('does not show a dead link for a network or server fault', async () => {
    vi.mocked(lookupVerification).mockRejectedValue(new Error('Network Error'))
    renderWithQuery(<VerifyEmailPage />)
    await waitFor(() => expect(lookupVerification).toHaveBeenCalled())
    expect(await screen.findByLabelText('Password')).toBeTruthy()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('finishes signing in with a fresh read of the account after enrolment', async () => {
    vi.mocked(verifyEmail).mockResolvedValue({ user: { id: 'u1' }, mfa_enrollment_needed: true } as never)
    vi.mocked(enrollMFAStart).mockResolvedValue({ secret: 'SECRET', qr_url: '', qr_data_url: 'data:,' })
    vi.mocked(enrollMFAConfirm).mockResolvedValue(undefined)
    vi.mocked(getMe).mockResolvedValue({ id: 'u1', mfa_enabled: true } as never)
    const user = userEvent.setup()
    renderWithQuery(<VerifyEmailPage />)
    await choose(user, 'correct-horse-battery')
    await user.type(await screen.findByLabelText('Verification code'), '123456')
    await waitFor(() => expect((screen.getByRole('button', { name: /confirm/i }) as HTMLButtonElement).disabled).toBe(false))
    await user.click(screen.getByRole('button', { name: /confirm/i }))

    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: '/dashboard' }))
    expect(getMe).toHaveBeenCalled()
    expect(useAuthStore.getState().user).toMatchObject({ mfa_enabled: true })
  })

  it('says a dead link is dead before a password is typed', async () => {
    vi.mocked(lookupVerification).mockRejectedValue(apiError('token_expired'))
    renderWithQuery(<VerifyEmailPage />)
    expect((await screen.findByRole('alert')).textContent).toMatch(/expired/i)
    expect(screen.queryByLabelText('Password')).toBeNull()
  })

  // #369: with MFA required, the verified session owes enrolment and every
  // other call is refused until it is done. The page used to go straight to
  // the dashboard anyway.
  it('goes to enrolment when the account must enrol, and to the dashboard only after', async () => {
    vi.mocked(verifyEmail).mockResolvedValue({ user: { id: 'u1' }, mfa_enrollment_needed: true } as never)
    vi.mocked(enrollMFAStart).mockResolvedValue({ secret: 'SECRET', qr_url: '', qr_data_url: 'data:,' })
    vi.mocked(enrollMFAConfirm).mockResolvedValue(undefined)
    vi.mocked(getMe).mockResolvedValue({ id: 'u1' } as never)
    const user = userEvent.setup()
    renderWithQuery(<VerifyEmailPage />)
    await choose(user, 'correct-horse-battery')

    const code = await screen.findByLabelText('Verification code')
    expect(navigate).not.toHaveBeenCalled()
    await user.type(code, '123456')
    await user.click(screen.getByRole('button', { name: /confirm/i }))

    await waitFor(() => expect(enrollMFAConfirm).toHaveBeenCalledWith('123456'))
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: '/dashboard' }))
  })

  it('sends the token with the chosen password, then signs in', async () => {
    vi.mocked(verifyEmail).mockResolvedValue({ user: { id: 'u1' } } as never)
    const user = userEvent.setup()
    renderWithQuery(<VerifyEmailPage />)
    await choose(user, 'correct-horse-battery')

    await waitFor(() => expect(verifyEmail).toHaveBeenCalledWith('tok-123', 'Alice', 'correct-horse-battery'))
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

  // #374: the name is asked for here, empty, never pre-filled: a pre-filled
  // name could only have come from a signup, which anyone can send.
  it('asks for the name, empty', async () => {
    renderWithQuery(<VerifyEmailPage />)
    const name = (await screen.findByLabelText('Display name')) as HTMLInputElement
    expect(name.value).toBe('')
    expect(name.required).toBe(true)
  })

  it('keeps the form after a blank name, so the link can still be used', async () => {
    vi.mocked(verifyEmail).mockRejectedValue(apiError('display_name_required', 'display name is required'))
    const user = userEvent.setup()
    renderWithQuery(<VerifyEmailPage />)
    await choose(user, 'correct-horse-battery')

    expect((await screen.findByRole('alert')).textContent).toMatch(/display name is required/i)
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
