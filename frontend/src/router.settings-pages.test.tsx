import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'
import { useAuthStore } from '@/store/auth'
import { router } from '@/router'
import { api } from '@/api/client'
import type { User } from '@/api/types'

// #674: each settings page is its own administrator-only address under
// /admin/settings, and the two old addresses redirect onto them.

const PAGES = [
  '/admin/settings/general',
  '/admin/settings/features',
  '/admin/settings/branding',
  '/admin/settings/email',
  '/admin/settings/email/templates',
  '/admin/settings/attachments',
  '/admin/settings/guest-access',
  '/admin/settings/authentication',
  '/admin/settings/privacy',
]

function signInAs(role: User['role']) {
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'someone@example.com', display_name: 'Someone', role, mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
}

type Route = { options: { beforeLoad?: (a: never) => unknown; component?: unknown } }
const routes = router.routesByPath as unknown as Record<string, Route>

// The guard as the router runs it. Typed loosely: beforeLoad is called with
// router context these guards do not read.
async function runGuard(path: string, args: object = {}) {
  const route = routes[path]
  expect(route, `no route registered at ${path}`).toBeTruthy()
  expect(route.options.beforeLoad, `${path} has no beforeLoad guard`).toBeTypeOf('function')
  return route.options.beforeLoad!(args as never)
}

// What a guard throws, or null when it lets the request through.
async function thrownBy(path: string, args: object = {}) {
  return runGuard(path, args).then(
    () => null,
    (e: { options: Record<string, unknown> }) => e.options,
  )
}

beforeEach(() => {
  useAuthStore.setState({ user: null })
  window.history.replaceState(null, '', '/')
})
afterEach(() => vi.restoreAllMocks())

describe('the settings page routes', () => {
  it('registers one route per page, each with its own component', () => {
    const components = PAGES.map((p) => routes[p]?.options.component)
    expect(components.every(Boolean)).toBe(true)
    expect(new Set(components).size).toBe(PAGES.length)
  })

  describe.each(PAGES)('%s', (path) => {
    it('lets an administrator in', async () => {
      signInAs('admin')
      expect(await thrownBy(path)).toBeNull()
    })

    it.each(['staff', 'user'] as const)('turns a %s back to the dashboard', async (role) => {
      signInAs(role)
      expect(await thrownBy(path)).toMatchObject({ to: '/dashboard' })
    })

    it('sends someone signed out to the login page', async () => {
      vi.spyOn(api, 'get').mockRejectedValue(new Error('401'))
      expect(await thrownBy(path)).toMatchObject({ to: '/login' })
    })
  })
})

describe('the old addresses', () => {
  it('sends /admin/settings to General, replacing the history entry', async () => {
    expect(await thrownBy('/admin/settings')).toMatchObject({ to: '/admin/settings/general', replace: true })
  })

  it('sends /admin/email-templates to the Templates tab, keeping the query', async () => {
    expect(await thrownBy('/admin/email-templates', { search: { t: 'ticket_created' } })).toMatchObject({
      to: '/admin/settings/email/templates',
      search: { t: 'ticket_created' },
      replace: true,
    })
  })

  it('does not look at who is signed in: the page they land on does', async () => {
    const get = vi.spyOn(api, 'get')
    expect(await thrownBy('/admin/settings')).not.toBeNull()
    expect(await thrownBy('/admin/email-templates')).not.toBeNull()
    expect(get).not.toHaveBeenCalled()
  })

  // End to end through the router: the address the browser ends on, and that
  // the new address replaces the old one in the history instead of being pushed
  // after it, so Back does not return to the redirect and loop.
  describe('as the router follows them', () => {
    async function land(from: string) {
      signInAs('admin')
      window.history.replaceState(null, '', from)
      const push = vi.spyOn(window.history, 'pushState')
      const replace = vi.spyOn(window.history, 'replaceState')
      await router.navigate({ href: from })
      const urls = (spy: typeof push) => spy.mock.calls.map((c) => String(c[2]))
      return { loc: router.state.location, pushed: urls(push), replaced: urls(replace) }
    }

    it('lands /admin/settings on /admin/settings/general, replacing the entry', async () => {
      const { loc, pushed, replaced } = await land('/admin/settings')
      expect(loc.pathname).toBe('/admin/settings/general')
      expect(window.location.pathname).toBe('/admin/settings/general')
      expect(pushed).not.toContain('/admin/settings/general')
      expect(replaced).toContain('/admin/settings/general')
    })

    it('lands /admin/email-templates?t=ticket_created on the Templates tab with ?t= kept', async () => {
      const { loc, pushed, replaced } = await land('/admin/email-templates?t=ticket_created')
      expect(loc.pathname).toBe('/admin/settings/email/templates')
      const to = '/admin/settings/email/templates?t=ticket_created'
      expect(window.location.pathname + window.location.search).toBe(to)
      expect(pushed).not.toContain(to)
      expect(replaced).toContain(to)
    })

    it('lands /admin/email-templates, with no query, on the Templates tab', async () => {
      const { loc } = await land('/admin/email-templates')
      expect(loc.pathname).toBe('/admin/settings/email/templates')
      expect(window.location.search).toBe('')
    })
  })
})
