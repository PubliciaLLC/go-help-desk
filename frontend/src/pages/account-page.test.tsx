import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { useAuthStore } from '@/store/auth'
import type { Passkey } from '@/api/passkeys'

// The account screen — the place a signed-in person manages their own
// sign-in. Before it existed there was nowhere to do any of this:
// `changePassword` sat in the API client with no caller at all, and second
// factor enrolment happened only as a forced step mid-login.

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
    browserSupportsPasskeys: vi.fn(() => true),
  }
})

vi.mock('@/api/auth', () => ({
  changePassword: vi.fn(),
  enrollMFAStart: vi.fn(),
  enrollMFAConfirm: vi.fn(),
  logout: vi.fn(),
}))

vi.mock('@/hooks/useSiteBranding', () => ({ useSiteBranding: () => ({ name: 'Help Desk', logoURL: null }) }))

import { AccountPage } from './AccountPage'
import { listPasskeys, addPasskey, removePasskey, browserSupportsPasskeys } from '@/api/passkeys'
import { changePassword, enrollMFAStart, enrollMFAConfirm } from '@/api/auth'

const SYNCED: Passkey = {
  id: 'pk-1',
  name: 'iPhone',
  transports: ['internal', 'hybrid'],
  backup_eligible: true,
  backup_state: true,
  created_at: '2026-09-01T10:00:00Z',
  last_used_at: '2026-09-20T10:00:00Z',
}

// The case the distinction actually turns on: a credential that CAN sync and
// currently is not. `backup_state` says no, `backup_eligible` says yes, and
// the honest label is Synced — a key that may appear in a cloud keychain at
// any moment is not a hardware-bound key. With every fixture agreeing on both
// flags, reading the wrong one would go unnoticed.
const ELIGIBLE_NOT_BACKED_UP: Passkey = {
  id: 'pk-3',
  name: 'Android phone',
  transports: ['internal'],
  backup_eligible: true,
  backup_state: false,
  created_at: '2026-09-03T10:00:00Z',
  last_used_at: null,
}

const HARDWARE: Passkey = {
  id: 'pk-2',
  name: '',
  transports: ['usb'],
  backup_eligible: false,
  backup_state: false,
  created_at: '2026-09-02T10:00:00Z',
  last_used_at: null,
}

function signedIn(mfa_enabled = false) {
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

beforeEach(() => {
  signedIn()
  vi.mocked(listPasskeys).mockResolvedValue([SYNCED, HARDWARE, ELIGIBLE_NOT_BACKED_UP])
  vi.mocked(browserSupportsPasskeys).mockReturnValue(true)
})

afterEach(() => vi.clearAllMocks())

describe('the passkey list', () => {
  it('separates a key that syncs from one bound to a device', async () => {
    renderWithQuery(<AccountPage />)
    await screen.findByText('iPhone')

    const synced = screen.getByText('iPhone').closest('li')!
    expect(within(synced).getByText('Synced')).toBeTruthy()

    // backup_eligible false — a hardware key. Getting this backwards would
    // tell someone their security key is in a cloud keychain.
    const hardware = screen.getByText(/Unnamed key/).closest('li')!
    expect(within(hardware).getByText('This device only')).toBeTruthy()

    // And the one that separates the two flags: eligible, not currently
    // backed up. Reading `backup_state` here would call it device-only.
    const eligible = screen.getByText('Android phone').closest('li')!
    expect(within(eligible).getByText('Synced')).toBeTruthy()
  })

  it('describes an unnamed key by its transports, never by its model', async () => {
    renderWithQuery(<AccountPage />)
    expect(await screen.findByText('Unnamed key (usb)')).toBeTruthy()
  })

  it('says so plainly when there are none', async () => {
    vi.mocked(listPasskeys).mockResolvedValue([])
    renderWithQuery(<AccountPage />)
    expect(await screen.findByText('No passkeys yet.')).toBeTruthy()
  })

  it('offers nothing when the browser cannot do WebAuthn', async () => {
    vi.mocked(browserSupportsPasskeys).mockReturnValue(false)
    renderWithQuery(<AccountPage />)
    expect(await screen.findByText('This browser does not support passkeys.')).toBeTruthy()
    // And it must not have asked the server for a list it cannot use.
    expect(listPasskeys).not.toHaveBeenCalled()
    expect(screen.queryByRole('button', { name: /Add a passkey/ })).toBeNull()
  })
})

describe('adding a passkey', () => {
  it('registers it under the name that was typed, and reloads the list', async () => {
    const user = userEvent.setup()
    vi.mocked(addPasskey).mockResolvedValue(undefined)
    renderWithQuery(<AccountPage />)
    await screen.findByText('iPhone')

    await user.type(screen.getByLabelText('Name this key'), '  Work laptop  ')
    await user.click(screen.getByRole('button', { name: 'Add a passkey' }))

    // Trimmed: a name with trailing spaces is a name the person cannot see
    // the end of, and it is stored verbatim.
    await waitFor(() => expect(addPasskey).toHaveBeenCalledWith('Work laptop'))
    await waitFor(() => expect(vi.mocked(listPasskeys).mock.calls.length).toBeGreaterThan(1))
  })

  it('shows nothing at all when the person dismisses the browser prompt', async () => {
    const user = userEvent.setup()
    vi.mocked(addPasskey).mockRejectedValue(new DOMException('', 'NotAllowedError'))
    renderWithQuery(<AccountPage />)
    await screen.findByText('iPhone')

    await user.click(screen.getByRole('button', { name: 'Add a passkey' }))

    // "The operation either timed out or was not allowed" reads as a fault.
    // They chose to stop; there is nothing to report.
    await waitFor(() => expect(addPasskey).toHaveBeenCalled())
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('does report a real failure', async () => {
    const user = userEvent.setup()
    vi.mocked(addPasskey).mockRejectedValue(new DOMException('', 'SecurityError'))
    renderWithQuery(<AccountPage />)
    await screen.findByText('iPhone')

    await user.click(screen.getByRole('button', { name: 'Add a passkey' }))
    await waitFor(() => expect(screen.getByRole('alert')).toBeTruthy())
  })
})

describe('removing a passkey', () => {
  it('asks first, names the key, and only then removes it', async () => {
    const user = userEvent.setup()
    vi.mocked(removePasskey).mockResolvedValue(undefined)
    renderWithQuery(<AccountPage />)
    await screen.findByText('iPhone')

    await user.click(screen.getByRole('button', { name: 'Remove iPhone' }))
    // Naming it matters: two keys in a list and an unnamed dialog is how the
    // wrong one gets removed.
    expect(await screen.findByText(/"iPhone" will stop working/)).toBeTruthy()
    expect(removePasskey).not.toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: 'Remove' }))
    await waitFor(() => expect(removePasskey).toHaveBeenCalledWith('pk-1'))
  })
})

describe('changing your password', () => {
  it('catches a mismatched confirmation before asking the server', async () => {
    const user = userEvent.setup()
    renderWithQuery(<AccountPage />)
    await screen.findByText('iPhone')

    await user.type(screen.getByLabelText('Current password'), 'old-one')
    await user.type(screen.getByLabelText('New password'), 'new-one')
    await user.type(screen.getByLabelText('Confirm new password'), 'new-two')
    await user.click(screen.getByRole('button', { name: 'Change password' }))

    expect(await screen.findByText('The new passwords do not match.')).toBeTruthy()
    // The server cannot know what was typed the second time, so it must not
    // be asked.
    expect(changePassword).not.toHaveBeenCalled()
  })

  it('sends both passwords when they match', async () => {
    const user = userEvent.setup()
    vi.mocked(changePassword).mockResolvedValue(undefined)
    renderWithQuery(<AccountPage />)
    await screen.findByText('iPhone')

    await user.type(screen.getByLabelText('Current password'), 'old-one')
    await user.type(screen.getByLabelText('New password'), 'new-one')
    await user.type(screen.getByLabelText('Confirm new password'), 'new-one')
    await user.click(screen.getByRole('button', { name: 'Change password' }))

    await waitFor(() => expect(changePassword).toHaveBeenCalledWith('old-one', 'new-one'))
    expect(await screen.findByText('Password changed.')).toBeTruthy()
  })
})

describe('the authenticator app section', () => {
  it('offers setup when there is none, and walks through the code', async () => {
    const user = userEvent.setup()
    vi.mocked(enrollMFAStart).mockResolvedValue({
      secret: 'JBSWY3DPEHPK3PXP',
      qr_url: 'otpauth://x',
      qr_data_url: 'data:image/png;base64,AAAA',
    })
    vi.mocked(enrollMFAConfirm).mockResolvedValue(undefined)
    renderWithQuery(<AccountPage />)
    await screen.findByText('iPhone')

    await user.click(screen.getByRole('button', { name: 'Set up an authenticator app' }))
    expect(await screen.findByText('JBSWY3DPEHPK3PXP')).toBeTruthy()
    expect(screen.getByAltText('Scan this code with your authenticator app')).toBeTruthy()

    await user.type(screen.getByLabelText('Enter the six-digit code to finish'), '123456')
    await user.click(screen.getByRole('button', { name: 'Finish setup' }))
    await waitFor(() => expect(enrollMFAConfirm).toHaveBeenCalledWith('123456'))

    // The store is updated so the section reflects reality without a reload.
    await waitFor(() => expect(useAuthStore.getState().user?.mfa_enabled).toBe(true))
  })

  it('offers no way to remove one, because the API has none', async () => {
    signedIn(true)
    renderWithQuery(<AccountPage />)
    await screen.findByText('iPhone')

    // Scoped to this section: the passkey list above has its own Remove
    // buttons, and a page-wide query would be answered by those.
    const totp = screen.getByRole('heading', { name: 'Authenticator app' }).closest('section')!
    expect(within(totp).getByText('Set up')).toBeTruthy()
    // A button here would be a button that cannot work. Stripping a second
    // factor is deliberately not something a session can do to its own
    // account; an administrator runs `go-help-desk reset-factors`.
    expect(within(totp).queryByRole('button', { name: /remove|disable|turn off/i })).toBeNull()
    expect(within(totp).getByText(/ask an administrator to reset your second factor/i)).toBeTruthy()
  })
})
