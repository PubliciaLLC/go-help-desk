import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError, type AxiosResponse } from 'axios'
import { renderWithQuery } from '@/test/render'
import { useAuthStore } from '@/store/auth'
import type { Passkey } from '@/api/passkeys'

// #336. Since #334 the routes that add or remove a second factor answer 403
// `mfa_required` to a session that has not proved one, and the message says to
// verify first. The only screen that called the verify routes was the login
// page, which offers them only when login itself asked for a factor. A user
// whose login asked for nothing (MFA off instance-wide, SSO without an amr
// claim, a session older than #334) got the message and nowhere to act on it.

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
  useRouterState: () => ({ location: { pathname: '/account' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

vi.mock('@/api/passkeys', async (orig) => {
  const actual = await orig<typeof import('@/api/passkeys')>()
  return {
    ...actual,
    listPasskeys: vi.fn(),
    addPasskey: vi.fn(),
    removePasskey: vi.fn(),
    signInWithPasskey: vi.fn(),
    browserSupportsPasskeys: vi.fn(() => true),
  }
})

vi.mock('@/api/auth', () => ({
  changePassword: vi.fn(),
  enrollMFAStart: vi.fn(),
  enrollMFAConfirm: vi.fn(),
  verifyMFA: vi.fn(),
  logout: vi.fn(),
}))

vi.mock('@/hooks/useSiteBranding', () => ({ useSiteBranding: () => ({ name: 'Help Desk', logoURL: null }) }))

import { AccountPage } from './AccountPage'
import { listPasskeys, addPasskey, removePasskey, signInWithPasskey } from '@/api/passkeys'
import { enrollMFAStart, verifyMFA } from '@/api/auth'

// What axios throws for an error response, so extractError and
// extractErrorCode read it the way they read the real thing.
function refused(status: number, code: string, message: string): AxiosError {
  return new AxiosError(message, String(status), undefined, undefined, {
    status,
    data: { error: { code, message } },
  } as AxiosResponse)
}

const MFA_REQUIRED = () =>
  refused(
    403,
    'mfa_required',
    'this account already has a second factor. Verify with it before adding or removing one, or ask an administrator to reset it.',
  )

const KEY: Passkey = {
  id: 'pk-1',
  name: 'iPhone',
  transports: ['internal'],
  backup_eligible: true,
  backup_state: true,
  created_at: '2026-09-01T10:00:00Z',
  last_used_at: null,
}

function signedIn(mfa_enabled: boolean) {
  useAuthStore.setState({
    user: {
      id: 'u-1',
      email: 'sam@example.com',
      display_name: 'Sam',
      role: 'staff',
      mfa_enabled,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })
}

const stepUp = () => screen.queryByRole('group', { name: 'Verify your second factor' })

// Removing a key asks first; this is the click through to the refusal.
async function removeIPhone(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole('button', { name: 'Remove iPhone' }))
  await user.click(await screen.findByRole('button', { name: 'Remove' }))
}

beforeEach(() => {
  signedIn(true)
  vi.mocked(listPasskeys).mockResolvedValue([])
})
afterEach(() => vi.clearAllMocks())

describe('adding a passkey to an account with an authenticator app', () => {
  it('asks for a code, verifies it, then adds the passkey', async () => {
    const user = userEvent.setup()
    vi.mocked(addPasskey).mockRejectedValueOnce(MFA_REQUIRED()).mockResolvedValue(undefined)
    vi.mocked(verifyMFA).mockResolvedValue(undefined)
    renderWithQuery(<AccountPage />)
    await screen.findByText('No passkeys yet.')

    await user.type(screen.getByLabelText('Name this key'), 'Work laptop')
    await user.click(screen.getByRole('button', { name: 'Add a passkey' }))

    const panel = await screen.findByRole('group', { name: 'Verify your second factor' })
    // Nothing is retried until the factor has been proved.
    expect(addPasskey).toHaveBeenCalledTimes(1)
    // No passkeys on the account, so a passkey is not on offer.
    expect(within(panel).queryByRole('button', { name: /passkey/i })).toBeNull()

    await user.type(within(panel).getByLabelText('Code from your authenticator app'), '123456')
    await user.click(within(panel).getByRole('button', { name: 'Verify' }))

    await waitFor(() => expect(verifyMFA).toHaveBeenCalledWith('123456'))
    // Retried with what the person had already typed.
    await waitFor(() => expect(addPasskey).toHaveBeenCalledTimes(2))
    expect(addPasskey).toHaveBeenLastCalledWith('Work laptop')
    await waitFor(() => expect(stepUp()).toBeNull())
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('explains what is being asked, and does not also show the refusal as an error', async () => {
    const user = userEvent.setup()
    vi.mocked(addPasskey).mockRejectedValue(MFA_REQUIRED())
    renderWithQuery(<AccountPage />)
    await screen.findByText('No passkeys yet.')

    await user.click(screen.getByRole('button', { name: 'Add a passkey' }))
    const panel = await screen.findByRole('group', { name: 'Verify your second factor' })
    expect(within(panel).getByText(/verify .* before changing it/i)).toBeTruthy()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('keeps the panel and shows the reason when the code is wrong, and does not retry', async () => {
    const user = userEvent.setup()
    vi.mocked(addPasskey).mockRejectedValue(MFA_REQUIRED())
    vi.mocked(verifyMFA).mockRejectedValue(refused(401, 'invalid_mfa_code', 'invalid TOTP code'))
    renderWithQuery(<AccountPage />)
    await screen.findByText('No passkeys yet.')

    await user.click(screen.getByRole('button', { name: 'Add a passkey' }))
    const panel = await screen.findByRole('group', { name: 'Verify your second factor' })
    await user.type(within(panel).getByLabelText('Code from your authenticator app'), '000000')
    await user.click(within(panel).getByRole('button', { name: 'Verify' }))

    expect(await within(panel).findByText('invalid TOTP code')).toBeTruthy()
    expect(addPasskey).toHaveBeenCalledTimes(1)
    expect(stepUp()).not.toBeNull()
  })

  it('can be dismissed', async () => {
    const user = userEvent.setup()
    vi.mocked(addPasskey).mockRejectedValue(MFA_REQUIRED())
    renderWithQuery(<AccountPage />)
    await screen.findByText('No passkeys yet.')

    await user.click(screen.getByRole('button', { name: 'Add a passkey' }))
    const panel = await screen.findByRole('group', { name: 'Verify your second factor' })
    await user.click(within(panel).getByRole('button', { name: 'Cancel' }))
    expect(stepUp()).toBeNull()
    expect(verifyMFA).not.toHaveBeenCalled()
  })

  it('does not open for a refusal that is about something else', async () => {
    const user = userEvent.setup()
    vi.mocked(addPasskey).mockRejectedValue(refused(400, 'registration_refused', 'the passkey was refused'))
    renderWithQuery(<AccountPage />)
    await screen.findByText('No passkeys yet.')

    await user.click(screen.getByRole('button', { name: 'Add a passkey' }))
    expect(await screen.findByRole('alert')).toBeTruthy()
    expect(screen.getByText('the passkey was refused')).toBeTruthy()
    expect(stepUp()).toBeNull()
  })
})

describe('an account whose only second factor is a passkey', () => {
  beforeEach(() => {
    signedIn(false)
    vi.mocked(listPasskeys).mockResolvedValue([KEY])
  })

  it('verifies with the passkey, not a code, then starts the authenticator setup', async () => {
    const user = userEvent.setup()
    vi.mocked(enrollMFAStart)
      .mockRejectedValueOnce(MFA_REQUIRED())
      .mockResolvedValue({ secret: 'JBSWY3DPEHPK3PXP', qr_url: 'otpauth://x', qr_data_url: 'data:image/png;base64,AAAA' })
    vi.mocked(signInWithPasskey).mockResolvedValue(undefined)
    renderWithQuery(<AccountPage />)
    await screen.findByText('iPhone')

    await user.click(screen.getByRole('button', { name: 'Set up an authenticator app' }))
    const panel = await screen.findByRole('group', { name: 'Verify your second factor' })
    // There is no authenticator app, so a code field would be a dead end.
    expect(within(panel).queryByLabelText(/code/i)).toBeNull()

    await user.click(within(panel).getByRole('button', { name: 'Verify with a passkey' }))

    await waitFor(() => expect(signInWithPasskey).toHaveBeenCalledTimes(1))
    expect(verifyMFA).not.toHaveBeenCalled()
    expect(await screen.findByText('JBSWY3DPEHPK3PXP')).toBeTruthy()
    expect(enrollMFAStart).toHaveBeenCalledTimes(2)
    expect(stepUp()).toBeNull()
  })

  it('removing a passkey: verify, then remove the one that was chosen', async () => {
    const user = userEvent.setup()
    vi.mocked(removePasskey).mockRejectedValueOnce(MFA_REQUIRED()).mockResolvedValue(undefined)
    vi.mocked(signInWithPasskey).mockResolvedValue(undefined)
    renderWithQuery(<AccountPage />)
    await screen.findByText('iPhone')

    await removeIPhone(user)
    const panel = await screen.findByRole('group', { name: 'Verify your second factor' })
    expect(removePasskey).toHaveBeenCalledTimes(1)
    await user.click(within(panel).getByRole('button', { name: 'Verify with a passkey' }))

    await waitFor(() => expect(removePasskey).toHaveBeenCalledTimes(2))
    expect(removePasskey).toHaveBeenLastCalledWith('pk-1')
    await waitFor(() => expect(stepUp()).toBeNull())
  })

  it('shows nothing when the person dismisses the browser prompt, and stays open', async () => {
    const user = userEvent.setup()
    vi.mocked(removePasskey).mockRejectedValue(MFA_REQUIRED())
    vi.mocked(signInWithPasskey).mockRejectedValue(new DOMException('', 'NotAllowedError'))
    renderWithQuery(<AccountPage />)
    await screen.findByText('iPhone')

    await removeIPhone(user)
    const panel = await screen.findByRole('group', { name: 'Verify your second factor' })
    await user.click(within(panel).getByRole('button', { name: 'Verify with a passkey' }))

    await waitFor(() => expect(signInWithPasskey).toHaveBeenCalled())
    expect(screen.queryByRole('alert')).toBeNull()
    expect(removePasskey).toHaveBeenCalledTimes(1)
    expect(stepUp()).not.toBeNull()
  })

  it('reports a passkey the server refused', async () => {
    const user = userEvent.setup()
    vi.mocked(removePasskey).mockRejectedValue(MFA_REQUIRED())
    vi.mocked(signInWithPasskey).mockRejectedValue(refused(401, 'assertion_refused', 'the passkey could not be verified'))
    renderWithQuery(<AccountPage />)
    await screen.findByText('iPhone')

    await removeIPhone(user)
    const panel = await screen.findByRole('group', { name: 'Verify your second factor' })
    await user.click(within(panel).getByRole('button', { name: 'Verify with a passkey' }))

    expect(await within(panel).findByText('the passkey could not be verified')).toBeTruthy()
    expect(removePasskey).toHaveBeenCalledTimes(1)
  })
})

describe('an account with both an authenticator app and a passkey', () => {
  it('offers both ways to verify', async () => {
    const user = userEvent.setup()
    signedIn(true)
    vi.mocked(listPasskeys).mockResolvedValue([KEY])
    vi.mocked(removePasskey).mockRejectedValue(MFA_REQUIRED())
    renderWithQuery(<AccountPage />)
    await screen.findByText('iPhone')

    await removeIPhone(user)
    const panel = await screen.findByRole('group', { name: 'Verify your second factor' })
    expect(within(panel).getByLabelText('Code from your authenticator app')).toBeTruthy()
    expect(within(panel).getByRole('button', { name: 'Verify with a passkey' })).toBeTruthy()
  })
})
