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
import { UserDetailPage } from '@/pages/admin/UserDetailPage'
import { GroupsPage } from '@/pages/admin/GroupsPage'
import { CategoriesPage } from '@/pages/admin/CategoriesPage'
import { StatusesPage } from '@/pages/admin/StatusesPage'
import { RolesPage } from '@/pages/admin/RolesPage'
import { SettingsPage } from '@/pages/admin/SettingsPage'
import { TagsPage } from '@/pages/admin/TagsPage'
import { GuestTicketPage } from '@/pages/GuestTicketPage'

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

const adminUserDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/users/$id',
  beforeLoad: requireAdmin,
  component: UserDetailPage,
})

const adminGroupsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/groups',
  beforeLoad: requireAdmin,
  component: GroupsPage,
})

const adminRolesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/roles',
  beforeLoad: requireAdmin,
  component: RolesPage,
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

const adminTagsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/tags',
  beforeLoad: requireAdmin,
  component: TagsPage,
})

// ── Guest ─────────────────────────────────────────────────────────────────────
const submitRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/submit',
  component: GuestTicketPage,
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
    submitRoute,
    ticketsRoute,
    newTicketRoute,
    ticketDetailRoute,
    adminUsersRoute,
    adminUserDetailRoute,
    adminGroupsRoute,
    adminRolesRoute,
    adminCategoriesRoute,
    adminStatusesRoute,
    adminTagsRoute,
    adminSettingsRoute,
  ]),
})

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}
