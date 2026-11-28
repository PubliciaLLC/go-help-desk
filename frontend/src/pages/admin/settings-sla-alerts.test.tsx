import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// The SLA alerts setting (#670), on Settings -> Features under the SLA section.
// It gates the breach and at-risk events for both email and webhooks, and is
// off on an upgraded install.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/settings' } }),
  useBlocker: () => ({ status: 'idle' }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}))

import { EmailSettingsPage } from './settings/EmailSettingsPage'

function serve(settings: Record<string, unknown>) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/settings') return Promise.resolve({ data: settings })
    if (url === '/admin/security-warnings') return Promise.resolve({ data: { insecure_secrets: [] } })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

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
})
afterEach(() => vi.restoreAllMocks())

async function openFeatures() {
  renderWithQuery(<EmailSettingsPage />)
  await screen.findByRole('heading', { name: 'Email' })
  await waitFor(() => expect(screen.queryByRole('status', { name: 'Loading' })).toBeNull())
}

const alerts = () => screen.getByRole('switch', { name: 'SLA alerts' }) as HTMLButtonElement

describe('Settings -> Features -> SLA alerts', () => {
  it('is a switch in the SLA section, off when stored off', async () => {
    serve({ sla_enabled: true, sla_alerts_enabled: false })
    await openFeatures()
    expect(alerts().getAttribute('aria-checked')).toBe('false')
  })

  it('is on when stored on', async () => {
    serve({ sla_enabled: true, sla_alerts_enabled: true })
    await openFeatures()
    expect(alerts().getAttribute('aria-checked')).toBe('true')
  })

  it('is shown even while SLA tracking is off, since it is a separate setting', async () => {
    serve({ sla_enabled: false, sla_alerts_enabled: false })
    await openFeatures()
    expect(alerts()).toBeTruthy()
  })

  it('says it is off on an upgraded install and what it does not send', async () => {
    serve({ sla_enabled: true, sla_alerts_enabled: false })
    await openFeatures()
    const text = (document.body.textContent ?? '').replace(/\s+/g, ' ')
    expect(text).toMatch(/breached or has reached 80%/i)
    expect(text).toMatch(/while this was off/i)
  })

  it('saves only the switch that was flipped', async () => {
    serve({ sla_enabled: true, sla_alerts_enabled: false })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: undefined } as any)
    await openFeatures()

    await userEvent.click(alerts())
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))

    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patch).toHaveBeenCalledWith('/admin/settings', { sla_alerts_enabled: true })
  })
})
