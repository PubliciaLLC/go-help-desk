import { describe, expect, it, beforeEach } from 'vitest'
import { useAuthStore } from '@/store/auth'
import { router } from '@/router'
import type { User } from '@/api/types'

// /admin/audit is administrators only, like GET /api/v1/admin/audit. It was
// open to staff, scoped to their own tickets; they now read the history on
// each ticket instead. Hiding the nav entry is not a guard, so the route has
// to turn staff away itself, the same way the other admin pages do.

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

describe('the /admin/audit route', () => {
  beforeEach(() => useAuthStore.setState({ user: null }))

  it('lets an administrator in', async () => {
    signInAs('admin')
    await expect(runGuard('/admin/audit')).resolves.toBeUndefined()
  })

  it.each(['staff', 'user'] as const)('turns a %s back to the dashboard', async (role) => {
    signInAs(role)
    await expect(runGuard('/admin/audit')).rejects.toMatchObject({ options: { to: '/dashboard' } })
  })

  // The same answer a staff member gets from /admin/users, so the audit page
  // is not a special case.
  it('sends staff the same place as another admin page', async () => {
    signInAs('staff')
    const redirectOf = (path: string) =>
      runGuard(path).then(
        () => null,
        (e: { options: unknown }) => e.options,
      )
    const audit = await redirectOf('/admin/audit')
    expect(audit).not.toBeNull()
    expect(audit).toEqual(await redirectOf('/admin/users'))
  })
})
