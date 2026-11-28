import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'

// #451: the sign-in page offers "Forgot password?" when the server can send
// the link (password_reset on /auth/providers), and only then. An old-hash
// refusal (#493) points at it.

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))
vi.mock('@/api/auth', () => ({
  login: vi.fn(),
  verifyMFA: vi.fn(),
  getMe: vi.fn(),
  enrollMFAStart: vi.fn(),
  enrollMFAConfirm: vi.fn(),
  changePassword: vi.fn(),
  getSignupStatus: vi.fn().mockResolvedValue({ enabled: false, open_registration: false, saml_enabled: false }),
  getAuthProviders: vi.fn(),
}))
vi.mock('@/api/admin', () => ({ getSiteConfig: vi.fn().mockResolvedValue({}) }))
vi.mock('@/api/passkeys', () => ({ signInWithPasskey: vi.fn(), wasCancelled: () => false }))
vi.mock('@/hooks/useSiteBranding', () => ({ useSiteBranding: () => ({ name: 'Help Desk', logoURL: null }) }))

import { LoginPage } from './LoginPage'
import { login, getAuthProviders } from '@/api/auth'

beforeEach(() => {
  window.history.replaceState({}, '', '/login')
})
afterEach(() => vi.clearAllMocks())

function providers(passwordReset: boolean | undefined) {
  vi.mocked(getAuthProviders).mockResolvedValue({
    providers: [{ name: 'local', enabled: true }],
    password_reset: passwordReset,
  })
}

describe('the sign-in page', () => {
  it('links to Forgot password when the server can send the link', async () => {
    providers(true)
    renderWithQuery(<LoginPage />)
    const link = await screen.findByRole('link', { name: /forgot password\?/i })
    expect(link.getAttribute('href')).toBe('/forgot-password')
  })

  it.each([false, undefined])('has no link when password_reset is %s', async (value) => {
    providers(value)
    renderWithQuery(<LoginPage />)
    await waitFor(() => expect(getAuthProviders).toHaveBeenCalled())
    await screen.findByLabelText('Email')
    expect(screen.queryByRole('link', { name: /forgot password/i })).toBeNull()
  })

  it('still shows the link beside the refusal an old password hash gets', async () => {
    providers(true)
    vi.mocked(login).mockRejectedValue({
      isAxiosError: true,
      response: {
        status: 403,
        data: { error: { code: 'password_reset_required', message: 'Your password was saved in an older, weaker form and must be reset before you can sign in. Choose "Forgot password" on the sign-in page and we will email you a link, or ask an administrator to reset it for you.' } },
      },
    })
    const user = userEvent.setup()
    renderWithQuery(<LoginPage />)
    await user.type(await screen.findByLabelText('Email'), 'ada@example.com')
    await user.type(screen.getByLabelText('Password'), 'old-password')
    await user.click(screen.getByRole('button', { name: /^sign in$/i }))

    expect((await screen.findByRole('alert')).textContent).toMatch(/must be reset/i)
    expect(screen.getByRole('link', { name: /forgot password\?/i }).getAttribute('href')).toBe('/forgot-password')
  })
})
