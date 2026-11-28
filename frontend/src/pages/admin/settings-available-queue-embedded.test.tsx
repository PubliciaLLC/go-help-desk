import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #676: the Available queue control sits inside the Settings page and has its
// own Save. Saving it changes the stored settings, so the settings dump comes
// back different; the page used to answer by replacing its whole unsaved draft
// with it, silently throwing away every other edit made on the page.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/settings' } }),
  useBlocker: () => ({ status: 'idle' }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

import { PrivacySettingsPage } from './settings/PrivacySettingsPage'

beforeEach(() => {
  vi.restoreAllMocks()
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'admin@example.com', display_name: 'Ada Admin', role: 'admin',
      mfa_enabled: false, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
})

describe('the Available queue Save inside the Settings page', () => {
  it('leaves other unsaved edits on the page alone', async () => {
    // The server's state: a PATCH of the roles changes what the next GET returns.
    let stored: Record<string, unknown> = { ticket_scope_enforced: false, available_queue_roles: ['admin'] }
    vi.spyOn(api, 'get').mockImplementation(((url: string) => {
      if (url === '/admin/settings') return Promise.resolve({ data: structuredClone(stored) })
      if (url === '/admin/security-warnings') return Promise.resolve({ data: { insecure_secrets: [] } })
      return Promise.resolve({ data: [] })
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    }) as any)
    const patch = vi.spyOn(api, 'patch').mockImplementation(((_url: string, body: Record<string, unknown>) => {
      stored = { ...stored, ...body }
      return Promise.resolve({ data: undefined })
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    }) as any)

    renderWithQuery(<PrivacySettingsPage />)
    await screen.findByRole('heading', { name: 'Privacy' })
    await waitFor(() => expect(screen.queryByRole('status', { name: 'Loading' })).toBeNull())

    const row = screen.getByText('Limit staff to their group scope').parentElement!.parentElement!
    const toggle = within(row).getByRole('switch')
    await userEvent.click(toggle)
    expect(toggle.getAttribute('aria-checked')).toBe('true')

    await userEvent.click(screen.getByRole('checkbox', { name: 'Staff' }))
    // The section's Save is the one next to its checkbox, not the page's.
    const section = screen.getByRole('heading', { name: 'Available queue' }).parentElement!
    await userEvent.click(within(section).getByRole('button', { name: /^save$/i }))

    await waitFor(() =>
      expect(patch).toHaveBeenCalledWith('/admin/settings', { available_queue_roles: ['admin', 'staff'] }),
    )
    // Let the refetch the save triggers land.
    await new Promise((r) => setTimeout(r, 100))

    expect(toggle.getAttribute('aria-checked')).toBe('true')
    // And the page's own Save still sends that edit.
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() =>
      expect(patch).toHaveBeenCalledWith('/admin/settings', { ticket_scope_enforced: true }),
    )
  })
})
