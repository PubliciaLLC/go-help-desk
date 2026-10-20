import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'
import { useAuthStore } from '@/store/auth'
import { router } from '@/router'
import { api } from '@/api/client'
import type { User } from '@/api/types'

// The post-upgrade wizard is for an administrator, once, on an install that
// was upgraded. "Upgraded" is "has no post_upgrade_130_seen": setup writes it
// on a fresh install, and the wizard writes it when it is finished or skipped.

function signInAs(role: User['role']) {
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'someone@example.com', display_name: 'Someone', role, mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
}

async function runGuard(path: string) {
  const route = (router.routesByPath as Record<string, { options: { beforeLoad?: (a: never) => unknown } }>)[path]
  expect(route, `no route registered at ${path}`).toBeTruthy()
  return route.options.beforeLoad!({} as never)
}

function serveSettings(settings: Record<string, unknown>) {
  return vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/settings') return Promise.resolve({ data: settings })
    return Promise.resolve({ data: {} })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

beforeEach(() => useAuthStore.setState({ user: null }))
afterEach(() => vi.restoreAllMocks())

describe('landing on the dashboard', () => {
  it('sends an administrator on an upgraded install to the wizard', async () => {
    signInAs('admin')
    serveSettings({ staff_email_assigned_override: 'off' })
    await expect(runGuard('/dashboard')).rejects.toMatchObject({ options: { to: '/after-upgrade' } })
  })

  it('does not once the wizard has been finished or skipped', async () => {
    signInAs('admin')
    serveSettings({ post_upgrade_130_seen: true })
    await expect(runGuard('/dashboard')).resolves.toBeUndefined()
  })

  it('does not on a fresh install, where setup recorded the answer', async () => {
    signInAs('admin')
    serveSettings({ post_upgrade_130_seen: true, update_check_enabled: false })
    await expect(runGuard('/dashboard')).resolves.toBeUndefined()
  })

  it.each(['staff', 'user'] as const)('never asks a %s, or even reads the settings', async (role) => {
    signInAs(role)
    const get = serveSettings({})
    await expect(runGuard('/dashboard')).resolves.toBeUndefined()
    expect(get).not.toHaveBeenCalledWith('/admin/settings')
  })

  it('lets an administrator through if the settings cannot be read', async () => {
    signInAs('admin')
    vi.spyOn(api, 'get').mockRejectedValue(new Error('down'))
    await expect(runGuard('/dashboard')).resolves.toBeUndefined()
  })
})

describe('the /after-upgrade route', () => {
  it('opens for an unasked administrator', async () => {
    signInAs('admin')
    serveSettings({})
    await expect(runGuard('/after-upgrade')).resolves.toBeUndefined()
  })

  it('turns an administrator who has answered back to the dashboard', async () => {
    signInAs('admin')
    serveSettings({ post_upgrade_130_seen: true })
    await expect(runGuard('/after-upgrade')).rejects.toMatchObject({ options: { to: '/dashboard' } })
  })

  it.each(['staff', 'user'] as const)('turns a %s away', async (role) => {
    signInAs(role)
    serveSettings({})
    await expect(runGuard('/after-upgrade')).rejects.toMatchObject({ options: { to: '/dashboard' } })
  })
})
