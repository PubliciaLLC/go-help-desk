import { describe, expect, it, beforeEach } from 'vitest'
import { useAuthStore } from '@/store/auth'
import { router } from '@/router'
import type { User } from '@/api/types'

// #131: /reports is for staff and administrators, as GET /reports/kpi is. A
// requester who types the address is sent to the dashboard, the same place a
// staff member typing an admin address goes. Hiding the nav item is not a
// guard, so the route has to turn them away itself.

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
  expect(route.options.beforeLoad, `${path} has no beforeLoad guard`).toBeTypeOf('function')
  return route.options.beforeLoad!({} as never)
}

describe('the /reports route', () => {
  beforeEach(() => useAuthStore.setState({ user: null }))

  it.each(['admin', 'staff'] as const)('lets %s in', async (role) => {
    signInAs(role)
    await expect(runGuard('/reports')).resolves.toBeUndefined()
  })

  it('turns a requester back to the dashboard', async () => {
    signInAs('user')
    await expect(runGuard('/reports')).rejects.toMatchObject({ options: { to: '/dashboard' } })
  })

  // The address is the state: what the router hands the page is already read.
  it('cleans the address it is given', () => {
    const route = (router.routesByPath as Record<string, { options: { validateSearch?: (s: Record<string, unknown>) => unknown } }>)['/reports']
    const validate = route.options.validateSearch
    expect(validate).toBeTypeOf('function')
    expect(validate!({ range: 7, priority: 'high', junk: 1 })).toEqual({ range: 7, priority: 'high' })
    expect(validate!({})).toEqual({ range: 30 })
  })
})
