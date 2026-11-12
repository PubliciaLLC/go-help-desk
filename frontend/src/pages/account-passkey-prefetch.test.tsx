import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError, type AxiosResponse } from 'axios'
import { renderWithQuery } from '@/test/render'
import { useAuthStore } from '@/store/auth'

// #632, second half. Safari wants the WebAuthn call inside the click with no
// network wait in front of it, so the page fetches the registration options
// ahead of time and the click hands them straight to addPasskey, which calls
// navigator.credentials.create before it awaits anything.

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
    prepareRegistration: vi.fn(),
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
import {
  listPasskeys,
  addPasskey,
  prepareRegistration,
  browserSupportsPasskeys,
  REGISTRATION_MAX_AGE_MS,
  type PreparedRegistration,
} from '@/api/passkeys'
import { verifyMFA } from '@/api/auth'

function refused(status: number, code: string, message: string): AxiosError {
  return new AxiosError(message, String(status), undefined, undefined, {
    status,
    data: { error: { code, message } },
  } as AxiosResponse)
}
const MFA_REQUIRED = () => refused(403, 'mfa_required', 'verify first')

let seq = 0
const prepared = (over: Partial<PreparedRegistration> = {}): PreparedRegistration => ({
  options: { challenge: new Uint8Array([++seq]) } as unknown as PublicKeyCredentialCreationOptions,
  fetchedAt: Date.now(),
  ...over,
})

const VERIFIED = { name: 'Verified — add the passkey' }

beforeEach(() => {
  seq = 0
  useAuthStore.setState({
    user: {
      id: 'u-1',
      email: 'sam@example.com',
      display_name: 'Sam',
      role: 'staff',
      mfa_enabled: true,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })
  vi.mocked(listPasskeys).mockResolvedValue([])
  vi.mocked(browserSupportsPasskeys).mockReturnValue(true)
  vi.mocked(prepareRegistration).mockImplementation(async () => prepared())
  vi.mocked(addPasskey).mockResolvedValue(undefined)
})
afterEach(() => vi.clearAllMocks())

describe('adding a passkey with options already fetched', () => {
  it('fetches the options when the page loads, and not before the list is known to be usable', async () => {
    renderWithQuery(<AccountPage />)
    await screen.findByText('No passkeys yet.')
    await waitFor(() => expect(prepareRegistration).toHaveBeenCalledTimes(1))
  })

  it('does not fetch them where passkeys are not on offer', async () => {
    vi.mocked(browserSupportsPasskeys).mockReturnValue(false)
    renderWithQuery(<AccountPage />)
    await screen.findByText('This browser does not support passkeys.')
    expect(prepareRegistration).not.toHaveBeenCalled()
  })

  it('does not fetch them when the server has passkeys off', async () => {
    vi.mocked(listPasskeys).mockRejectedValue(refused(503, 'passkeys_unavailable', 'off'))
    renderWithQuery(<AccountPage />)
    await screen.findByText(/Passkeys are not available on this instance/)
    expect(prepareRegistration).not.toHaveBeenCalled()
  })

  it('calls addPasskey inside the click, with the held options, with nothing awaited first', async () => {
    renderWithQuery(<AccountPage />)
    await screen.findByText('No passkeys yet.')
    await waitFor(() => expect(prepareRegistration).toHaveBeenCalled())
    // The fetch has to have landed in the hook, not merely been started.
    await new Promise((r) => setTimeout(r, 0))
    const held = await vi.mocked(prepareRegistration).mock.results[0].value

    fireEvent.change(screen.getByLabelText('Name this key'), { target: { value: 'Work laptop' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add a passkey' }))

    // No await between the click and this line: if the handler waited on
    // anything (a request, a mutation queue) this would still be zero.
    expect(addPasskey).toHaveBeenCalledTimes(1)
    expect(addPasskey).toHaveBeenCalledWith('Work laptop', held)
  })

  it('and again after a step-up: the button hands over options fetched when it was proved', async () => {
    const user = userEvent.setup()
    vi.mocked(prepareRegistration).mockRejectedValueOnce(MFA_REQUIRED())
    vi.mocked(addPasskey).mockRejectedValueOnce(MFA_REQUIRED()).mockResolvedValue(undefined)
    vi.mocked(verifyMFA).mockResolvedValue(undefined)
    renderWithQuery(<AccountPage />)
    await screen.findByText('No passkeys yet.')
    // The load-time fetch was refused, so nothing is held for the first click.
    await waitFor(() => expect(prepareRegistration).toHaveBeenCalledTimes(1))
    // And the refusal is not shown: the person has not asked for anything yet.
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.queryByRole('group', { name: 'Verify your second factor' })).toBeNull()

    await user.click(screen.getByRole('button', { name: 'Add a passkey' }))
    const panel = await screen.findByRole('group', { name: 'Verify your second factor' })
    // Nothing was held, so this click fetched inside itself, as before.
    expect(vi.mocked(addPasskey).mock.calls[0]).toEqual([''])

    await user.type(within(panel).getByLabelText('Code from your authenticator app'), '123456')
    await user.click(within(panel).getByRole('button', { name: 'Verify' }))
    const button = await screen.findByRole('button', VERIFIED)
    // Proving the factor is what lets the server stage a challenge, so that is
    // when the page asks for one.
    await waitFor(() => expect(prepareRegistration).toHaveBeenCalledTimes(2))
    await new Promise((r) => setTimeout(r, 0))
    const held = await vi.mocked(prepareRegistration).mock.results[1].value

    fireEvent.click(button)

    expect(addPasskey).toHaveBeenCalledTimes(2)
    expect(vi.mocked(addPasskey).mock.calls[1][1]).toBe(held)
  })

  it('falls back to starting inside the click when what it holds has gone stale', async () => {
    vi.mocked(prepareRegistration).mockImplementation(async () =>
      prepared({ fetchedAt: Date.now() - REGISTRATION_MAX_AGE_MS - 1000 }),
    )
    renderWithQuery(<AccountPage />)
    await screen.findByText('No passkeys yet.')
    await waitFor(() => expect(prepareRegistration).toHaveBeenCalled())
    await new Promise((r) => setTimeout(r, 0))

    fireEvent.change(screen.getByLabelText('Name this key'), { target: { value: 'Work laptop' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add a passkey' }))

    // Not refused and not stuck: addPasskey is called with the name alone and
    // fetches for itself, which is what the page did before this change.
    expect(addPasskey).toHaveBeenCalledTimes(1)
    expect(addPasskey).toHaveBeenCalledWith('Work laptop')
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull())
  })

  it('fetches new options after an attempt, so a retry has some', async () => {
    vi.mocked(addPasskey).mockRejectedValueOnce(new DOMException('', 'SecurityError'))
    renderWithQuery(<AccountPage />)
    await screen.findByText('No passkeys yet.')
    await waitFor(() => expect(prepareRegistration).toHaveBeenCalledTimes(1))
    await new Promise((r) => setTimeout(r, 0))

    fireEvent.click(screen.getByRole('button', { name: 'Add a passkey' }))

    await screen.findByRole('alert')
    await waitFor(() => expect(prepareRegistration).toHaveBeenCalledTimes(2))
  })
})
