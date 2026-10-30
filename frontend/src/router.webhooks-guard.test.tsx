import { describe, expect, it, beforeEach } from 'vitest'
import { useAuthStore } from '@/store/auth'
import { router } from '@/router'
import type { User } from '@/api/types'

// #157: /admin/webhooks is administrator-only, like the other credential
// pages. The nav entry is hidden from everyone else, but hiding a link is not
// a guard: the route itself has to turn a staff member or a plain user away.

function signInAs(role: User['role']) {
  useAuthStore.setState({
    user: {
      id: 'u-1',
      email: 'someone@example.com',
      display_name: 'Someone',
      role,
      mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })
}

// The guard as the router runs it. Typed loosely: beforeLoad is called with
// router context this guard does not read.
async function runGuard(path: string) {
  const route = (router.routesByPath as Record<string, { options: { beforeLoad?: (a: never) => unknown } }>)[path]
  expect(route, `no route registered at ${path}`).toBeTruthy()
  expect(route.options.beforeLoad, `${path} has no beforeLoad guard`).toBeTypeOf('function')
  return route.options.beforeLoad!({} as never)
}

describe('the /admin/webhooks route', () => {
  beforeEach(() => useAuthStore.setState({ user: null }))

  it('lets an administrator in', async () => {
    signInAs('admin')
    await expect(runGuard('/admin/webhooks')).resolves.toBeUndefined()
  })

  it.each(['staff', 'user'] as const)('turns a %s back to the dashboard', async (role) => {
    signInAs(role)
    await expect(runGuard('/admin/webhooks')).rejects.toMatchObject({
      options: { to: '/dashboard' },
    })
  })
})
