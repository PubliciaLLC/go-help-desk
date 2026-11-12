import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'

// #451: "Forgot password". The page says the same thing whether or not the
// address has an account, because the server does and a page that differed
// would undo it.

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))
vi.mock('@/api/auth', () => ({ requestPasswordReset: vi.fn() }))

import { ForgotPasswordPage } from './ForgotPasswordPage'
import { requestPasswordReset } from '@/api/auth'

function apiError(status: number, code: string, message = code) {
  return { isAxiosError: true, response: { status, data: { error: { code, message } } } }
}

beforeEach(() => {
  vi.mocked(requestPasswordReset).mockResolvedValue(undefined)
})
afterEach(() => vi.clearAllMocks())

async function ask(address: string) {
  const user = userEvent.setup()
  renderWithQuery(<ForgotPasswordPage />)
  await user.type(screen.getByLabelText('Email'), address)
  await user.click(screen.getByRole('button', { name: /send reset link/i }))
}

describe('the forgot password page', () => {
  it('asks for the address and sends it', async () => {
    await ask('ada@example.com')
    await waitFor(() => expect(requestPasswordReset).toHaveBeenCalledWith('ada@example.com'))
  })

  it('says the same thing whatever the address', async () => {
    const first = renderWithQuery(<ForgotPasswordPage />)
    const user = userEvent.setup()
    await user.type(first.getByLabelText('Email'), 'known@example.com')
    await user.click(first.getByRole('button', { name: /send reset link/i }))
    const known = (await first.findByRole('status')).textContent
    first.unmount()

    const second = renderWithQuery(<ForgotPasswordPage />)
    await user.type(second.getByLabelText('Email'), 'nobody@example.com')
    await user.click(second.getByRole('button', { name: /send reset link/i }))
    const unknown = (await second.findByRole('status')).textContent

    expect(known).toBeTruthy()
    expect(unknown).toBe(known)
    expect(known).toMatch(/if that address has an account/i)
  })

  it('offers the way back to sign in', async () => {
    await ask('ada@example.com')
    const back = await screen.findByRole('link', { name: /back to sign in/i })
    expect(back.getAttribute('href')).toBe('/login')
  })

  it('says so when the server cannot send email', async () => {
    vi.mocked(requestPasswordReset).mockRejectedValue(apiError(503, 'email_not_configured', 'password reset needs email'))
    await ask('ada@example.com')
    expect((await screen.findByRole('alert')).textContent).toMatch(/administrator/i)
    expect(screen.queryByRole('status')).toBeNull()
  })

  it('says to wait when it is asked too often', async () => {
    vi.mocked(requestPasswordReset).mockRejectedValue(apiError(429, 'rate_limited', 'too many attempts; please wait and try again'))
    await ask('ada@example.com')
    expect((await screen.findByRole('alert')).textContent).toMatch(/too many/i)
  })
})
