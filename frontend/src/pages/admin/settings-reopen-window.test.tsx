import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #349: reopen_window_days is also when a Resolved ticket closes, and a closed
// ticket is read-only for everyone who filed it. The screen has to say so,
// including what 0 means, because 0 reads as "off" and is not: it closes a
// Resolved ticket on the next sweep.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/settings' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}))

import { SettingsPage } from './SettingsPage'

beforeEach(() => {
  useAuthStore.setState({
    user: {
      id: 'u-1',
      email: 'admin@example.com',
      display_name: 'Ada Admin',
      role: 'admin',
      mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/settings') return Promise.resolve({ data: { reopen_window_days: 7 } })
    if (url === '/admin/security-warnings') return Promise.resolve({ data: { insecure_secrets: [] } })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
})

describe('the reopen window setting', () => {
  it('says it is also when the ticket closes and becomes read-only', async () => {
    renderWithQuery(<SettingsPage />)
    await screen.findByRole('heading', { name: 'Settings' })

    expect(screen.getByText('Reopen window and auto-close')).toBeTruthy()
    const help = screen.getByText(/also when the ticket closes/i)
    expect(help.textContent).toMatch(/read-only/i)
    expect(help.textContent).toMatch(/cannot be reopened by anyone/i)
  })

  it('says what 0 means: closed and read-only on the next sweep, not "off"', async () => {
    renderWithQuery(<SettingsPage />)
    await screen.findByRole('heading', { name: 'Settings' })

    const help = screen.getByText(/also when the ticket closes/i)
    expect(help.textContent).toMatch(/Set to 0 for no reopening/i)
    expect(help.textContent).toMatch(/closed and read-only on the next automatic sweep/i)
    // The old text promised something 0 never delivered.
    expect(help.textContent).not.toMatch(/prevent reopening entirely/i)
  })
})
