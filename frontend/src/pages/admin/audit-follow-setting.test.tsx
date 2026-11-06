import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #441: whether staff see the followed / unfollowed entries in the audit
// trail. Off by default; administrators always see them.

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
  return patch
}

const toggle = () => screen.getByRole('switch', { name: /staff can view follow history/i })

describe('the follow history setting', () => {
  it('is off by default', async () => {
    await renderSettings({})
    expect(toggle().getAttribute('aria-checked')).toBe('false')
  })

  it('shows what the instance has stored', async () => {
    await renderSettings({ staff_can_view_follow_audit: true })
    expect(toggle().getAttribute('aria-checked')).toBe('true')
  })

  it('is sent under its own key', async () => {
    const patch = await renderSettings({ staff_can_view_follow_audit: false })
    await userEvent.click(toggle())
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))

    await waitFor(() => expect(patch).toHaveBeenCalled())
    const call = patch.mock.calls[patch.mock.calls.length - 1]
    expect(call[0]).toBe('/admin/settings')
    expect(call[1]).toMatchObject({ staff_can_view_follow_audit: true })
  })
})
