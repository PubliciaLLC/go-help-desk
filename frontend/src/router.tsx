import { createRouter, createRoute, createRootRoute, redirect } from '@tanstack/react-router'
import { getMe } from '@/api/auth'
import { getSetupStatus } from '@/api/setup'
import { useAuthStore } from '@/store/auth'
import { LoginPage } from '@/pages/LoginPage'
import { SetupPage } from '@/pages/SetupPage'
import { DashboardPage } from '@/pages/DashboardPage'
import { TicketListPage } from '@/pages/TicketListPage'
import { NewTicketPage } from '@/pages/NewTicketPage'
import { TicketDetailPage } from '@/pages/TicketDetailPage'
import { UsersPage } from '@/pages/admin/UsersPage'
import { CategoriesPage } from '@/pages/admin/CategoriesPage'
import { StatusesPage } from '@/pages/admin/StatusesPage'
import { SettingsPage } from '@/pages/admin/SettingsPage'

async function requireAuth() {
  const { user, setUser } = useAuthStore.getState()
  if (user) return
  try {
    const me = await getMe()
    setUser(me)
  } catch {
    throw redirect({ to: '/login' })
  }
}

async function requireAdmin() {
  await requireAuth()
  const { user } = useAuthStore.getState()
  if (user?.role !== 'admin') throw redirect({ to: '/dashboard' })
}

// ── Root ──────────────────────────────────────────────────────────────────────
const rootRoute = createRootRoute()

// ── Public ────────────────────────────────────────────────────────────────────
const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/login',
  component: LoginPage,
})

// Only accessible when no users exist yet; redirects to /login otherwise.
const setupRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/setup',
  beforeLoad: async () => {
    const { needed } = await getSetupStatus()
    if (!needed) throw redirect({ to: '/login' })
  },
  component: SetupPage,
})

// ── Authenticated ─────────────────────────────────────────────────────────────
const dashboardRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/dashboard',
  beforeLoad: requireAuth,
  component: DashboardPage,
})

const ticketsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/tickets',
  beforeLoad: requireAuth,
  component: TicketListPage,
})

const newTicketRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/tickets/new',
  beforeLoad: requireAuth,
  component: NewTicketPage,
})

const ticketDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/tickets/$id',
  beforeLoad: requireAuth,
  component: TicketDetailPage,
})

// ── Admin ─────────────────────────────────────────────────────────────────────
const adminUsersRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/users',
  beforeLoad: requireAdmin,
  component: UsersPage,
})

const adminCategoriesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/categories',
  beforeLoad: requireAdmin,
  component: CategoriesPage,
})

const adminStatusesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/statuses',
  beforeLoad: requireAdmin,
  component: StatusesPage,
})

const adminSettingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/settings',
  beforeLoad: requireAdmin,
  component: SettingsPage,
})

// ── Index redirect ────────────────────────────────────────────────────────────
const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  beforeLoad: async () => {
    const { needed } = await getSetupStatus()
    throw redirect({ to: needed ? '/setup' : '/dashboard' })
  },
  component: () => null,
})

export const router = createRouter({
  routeTree: rootRoute.addChildren([
    indexRoute,
    loginRoute,
    setupRoute,
    dashboardRoute,
    ticketsRoute,
    newTicketRoute,
    ticketDetailRoute,
    adminUsersRoute,
    adminCategoriesRoute,
    adminStatusesRoute,
    adminSettingsRoute,
  ]),
})

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}
