import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #616: roles that must use a passkey. Ticking one makes MFA enforcement for
// that role a fact the page shows (checked, dimmed) without writing it: the
// stored mfa_enforced_roles is never touched, so unticking gives it back.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/settings' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

import { SettingsPage } from './SettingsPage'

beforeEach(() => {
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'admin@example.com', display_name: 'Ada Admin', role: 'admin', mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
})
afterEach(() => vi.restoreAllMocks())

async function renderSettings(settings: Record<string, unknown>) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/settings') return Promise.resolve({ data: settings })
    if (url === '/admin/security-warnings') return Promise.resolve({ data: { insecure_secrets: [] } })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: undefined } as any)
  renderWithQuery(<SettingsPage />)
  await screen.findByRole('heading', { name: 'Settings' })
  await userEvent.click(screen.getByRole('button', { name: /authentication/i }))
  return patch
}

const mfaBox = (role: string) => screen.getByRole('checkbox', { name: new RegExp(`^${role}$`, 'i') }) as HTMLInputElement
const passkeyBox = (role: string) => screen.getByRole('checkbox', { name: `Require passkeys for ${role}` }) as HTMLInputElement

describe('Require passkeys for roles', () => {
  it('is hidden while MFA is off, like the other role list', async () => {
    await renderSettings({ mfa_enabled: false })
    expect(screen.queryByRole('checkbox', { name: /require passkeys for/i })).toBeNull()
  })

  it('shows what the instance has stored', async () => {
    await renderSettings({ mfa_enabled: true, mfa_enforced_roles: [], passkey_required_roles: ['staff'] })
    expect(passkeyBox('staff').checked).toBe(true)
    expect(passkeyBox('admin').checked).toBe(false)
    expect(passkeyBox('user').checked).toBe(false)
  })

  it('shows the MFA box of a passkey-required role checked and disabled', async () => {
    await renderSettings({ mfa_enabled: true, mfa_enforced_roles: [], passkey_required_roles: ['staff'] })
    expect(mfaBox('staff').checked).toBe(true)
    expect(mfaBox('staff').disabled).toBe(true)
    expect(mfaBox('admin').checked).toBe(false)
    expect(mfaBox('admin').disabled).toBe(false)
  })

  it('shows the box checked and disabled as soon as the role is ticked, and restores the stored value when unticked', async () => {
    await renderSettings({ mfa_enabled: true, mfa_enforced_roles: ['admin'], passkey_required_roles: [] })
    expect(mfaBox('staff').checked).toBe(false)

    await userEvent.click(passkeyBox('staff'))
    expect(mfaBox('staff').checked).toBe(true)
    expect(mfaBox('staff').disabled).toBe(true)

    await userEvent.click(passkeyBox('staff'))
    expect(mfaBox('staff').checked).toBe(false)
    expect(mfaBox('staff').disabled).toBe(false)
    // A role that was enforced on its own stays so, ticked or not.
    await userEvent.click(passkeyBox('admin'))
    await userEvent.click(passkeyBox('admin'))
    expect(mfaBox('admin').checked).toBe(true)
    expect(mfaBox('admin').disabled).toBe(false)
  })

  it('sends passkey_required_roles and leaves mfa_enforced_roles untouched', async () => {
    const patch = await renderSettings({ mfa_enabled: true, mfa_enforced_roles: ['admin'], passkey_required_roles: [] })
    await userEvent.click(passkeyBox('staff'))
    await userEvent.click(screen.getAllByRole('button', { name: /save changes/i }).at(-1)!)
    await waitFor(() => expect(patch).toHaveBeenCalled())
    const body = patch.mock.calls[patch.mock.calls.length - 1][1] as Record<string, unknown>
    expect(body.passkey_required_roles).toEqual(['staff'])
    expect('mfa_enforced_roles' in body).toBe(false)
  })
})
