import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// The ticket list reads sla_enabled from GET /me. Saving Settings is how an
// administrator changes it, so a save has to make that answer stale.

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
    if (url === '/admin/settings') return Promise.resolve({ data: { sla_enabled: false } })
    if (url === '/admin/security-warnings') return Promise.resolve({ data: { insecure_secrets: [] } })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
})
afterEach(() => vi.restoreAllMocks())

describe('saving Settings', () => {
  it('makes the cached /me stale, so the ticket list picks up a changed SLA switch', async () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: undefined } as any)
    const { queryClient } = renderWithQuery(<SettingsPage />)
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')
    await screen.findByRole('heading', { name: 'Settings' })

    await userEvent.click(screen.getByRole('button', { name: 'Features' }))
    await userEvent.click(screen.getAllByRole('switch')[0])
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))

    await waitFor(() => expect(patch).toHaveBeenCalledWith('/admin/settings', { sla_enabled: true }))
    await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: ['me'] }))
  })
})
