import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import type { LoginResponse } from '@/api/auth'

// SSO error handling: #401 adds ?error= to /login after SAML and OIDC
// refusals. The page maps each code to a message and shows it, never
// rendering text from the query string.

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
  getAuthProviders: vi.fn(),
}))
vi.mock('@/api/admin', () => ({ getSiteConfig: vi.fn().mockResolvedValue({}) }))
vi.mock('@/api/passkeys', () => ({
  signInWithPasskey: vi.fn(),
  wasCancelled: (e: unknown) => e instanceof DOMException && (e.name === 'NotAllowedError' || e.name === 'AbortError'),
}))
vi.mock('@/hooks/useSiteBranding', () => ({ useSiteBranding: () => ({ name: 'Help Desk', logoURL: null }) }))

import { LoginPage } from './LoginPage'
import { login, getMe, getAuthProviders } from '@/api/auth'

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

function renderAt(search: string) {
  window.history.replaceState({}, '', '/login' + search)
  return renderWithQuery(<LoginPage />)
}

beforeEach(() => {
  vi.mocked(getMe).mockResolvedValue(USER)
  vi.mocked(login).mockResolvedValue(answer({}))
  vi.mocked(getAuthProviders).mockResolvedValue({ providers: [{ name: 'password', enabled: true }] })
})

afterEach(() => {
  vi.clearAllMocks()
  window.history.replaceState({}, '', '/')
})

describe('SSO error messages', () => {
  it.each([
    ['email_taken', 'Another account here already uses the email address your identity provider sent. Ask an administrator to sort it out.'],
    ['domain_not_allowed', 'Accounts from your email domain cannot sign in here.'],
    ['account_disabled', 'This account is disabled. Ask an administrator if you need access.'],
    ['account_link_refused', 'Your single sign-on identity could not be linked to the existing account with that email address. Ask an administrator.'],
    ['invalid_assertion', 'Your identity provider did not send the details this help desk needs. Ask an administrator to check the single sign-on setup.'],
    ['email_not_verified', 'Your identity provider did not send a verified email address, so you could not be signed in.'],
    ['invalid_email', 'Your identity provider sent an email address this help desk cannot use. Ask an administrator.'],
    ['sso_session_used', 'That sign-in has already been used. Please sign in again.'],
    ['invalid_session', 'Your sign-in expired or was started in another window. Please try again.'],
    ['invalid_state', 'Your sign-in expired or was started in another window. Please try again.'],
    ['idp_error', 'Your identity provider did not allow the sign-in.'],
    ['missing_code', 'The response from your identity provider could not be checked. Please try again.'],
    ['missing_id_token', 'The response from your identity provider could not be checked. Please try again.'],
    ['invalid_id_token', 'The response from your identity provider could not be checked. Please try again.'],
    ['internal_error', 'Something went wrong while signing you in. Please try again.'],
  ])('shows the message for code %s', async (code, message) => {
    renderAt(`?error=${code}`)
    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toBe(message)
  })

  it('an unknown code shows the generic message', async () => {
    renderAt('?error=something_new')
    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toBe('Single sign-on did not finish. Please try again, or ask an administrator.')
  })

  it.each(['constructor', '__proto__', 'toString', 'hasOwnProperty'])('codes that are names on Object.prototype show the generic message: %s', async (code) => {
    renderAt(`?error=${code}`)
    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toBe('Single sign-on did not finish. Please try again, or ask an administrator.')
  })

  it('a script-looking code renders nothing from the query string', async () => {
    const xss = encodeURIComponent('<img src=x onerror="window.__pwned=1">')
    renderAt(`?error=${xss}`)
    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toBe('Single sign-on did not finish. Please try again, or ask an administrator.')
    expect(document.querySelector('img')).toBeNull()
    expect(alert.textContent).not.toContain('<img')
    expect(alert.textContent).not.toContain('onerror')
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    expect((window as any).__pwned).toBeUndefined()
  })

  it('no error parameter, no alert', async () => {
    renderAt('')
    await screen.findByLabelText(/email/i)
    expect(screen.queryByRole('alert')).toBeNull()
  })
})

describe('SAML button', () => {
  it('is shown when SAML is enabled, as a link to the SAML login', async () => {
    vi.mocked(getAuthProviders).mockResolvedValue({
      providers: [
        { name: 'local', enabled: true },
        { name: 'saml', enabled: true },
      ],
    })
    renderAt('')
    const link = await screen.findByRole('link', { name: 'Sign in with SAML' })
    expect(link.getAttribute('href')).toBe('/api/v1/auth/saml/login')
  })

  it('is hidden when SAML is not enabled', async () => {
    vi.mocked(getAuthProviders).mockResolvedValue({
      providers: [{ name: 'saml', enabled: false }],
    })
    renderAt('')
    await screen.findByLabelText(/email/i)
    await waitFor(() => expect(getAuthProviders).toHaveBeenCalled())
    expect(screen.queryByRole('link', { name: /saml/i })).toBeNull()
  })

  it('is also hidden when SAML is not listed at all', async () => {
    vi.mocked(getAuthProviders).mockResolvedValue({
      providers: [{ name: 'local', enabled: true }],
    })
    renderAt('')
    await screen.findByLabelText(/email/i)
    await waitFor(() => expect(getAuthProviders).toHaveBeenCalled())
    expect(screen.queryByRole('link', { name: /saml/i })).toBeNull()
  })

  it('SAML and OIDC buttons both appear when both are enabled', async () => {
    vi.mocked(getAuthProviders).mockResolvedValue({
      providers: [
        { name: 'local', enabled: true },
        { name: 'saml', enabled: true },
        { name: 'oidc', enabled: true },
      ],
    })
    renderAt('')
    await screen.findByLabelText(/email/i)
    expect(await screen.findByRole('button', { name: /oidc/i })).toBeTruthy()
    expect(await screen.findByRole('link', { name: /saml/i })).toBeTruthy()
  })
})

describe('message clearing', () => {
  it('starting a password sign-in clears the message', async () => {
    vi.mocked(login).mockResolvedValue(answer({ mfa_needed: true }))
    const user = userEvent.setup()
    renderAt('?error=email_taken')

    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toContain('Another account here already uses the email address')

    await user.type(await screen.findByLabelText(/email/i), 'sam@example.com')
    await user.type(screen.getByLabelText(/password/i), 'hunter2')
    await user.click(screen.getByRole('button', { name: /sign in/i }))

    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull())
  })
})
