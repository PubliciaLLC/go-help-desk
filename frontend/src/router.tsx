import { createRouter, createRoute, createRootRoute, redirect } from '@tanstack/react-router'
import { getMe } from '@/api/auth'
import { getSetupStatus } from '@/api/setup'
import { getSettings } from '@/api/admin'
import { needsAfterUpgrade } from '@/lib/afterUpgrade'
import { AfterUpgradePage } from '@/pages/AfterUpgradePage'
import { useAuthStore } from '@/store/auth'
import { LoginPage } from '@/pages/LoginPage'
import { SetupPage } from '@/pages/SetupPage'
import { AccountPage } from '@/pages/AccountPage'
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
import { CannedResponsesPage } from '@/pages/admin/CannedResponsesPage'
import { CustomFieldsPage } from '@/pages/admin/CustomFieldsPage'
import { APIKeysPage } from '@/pages/admin/APIKeysPage'
import { AdminAuditPage } from '@/pages/admin/AdminAuditPage'
import { OAuthClientsPage } from '@/pages/admin/OAuthClientsPage'
import { WebhooksPage } from '@/pages/admin/WebhooksPage'
import { EmailTemplatesPage } from '@/pages/admin/EmailTemplatesPage'
import { RoutingPage } from '@/pages/admin/RoutingPage'
import { GuestTicketPage } from '@/pages/GuestTicketPage'
import { GuestTicketViewPage } from '@/pages/GuestTicketViewPage'
import { GuestTrackPage } from '@/pages/GuestTrackPage'
import { SignupPage } from '@/pages/SignupPage'
import { VerifyEmailPage } from '@/pages/VerifyEmailPage'
import { ForgotPasswordPage } from '@/pages/ForgotPasswordPage'
import { ResetPasswordPage } from '@/pages/ResetPasswordPage'

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

async function requireStaffOrAdmin() {
  await requireAuth()
  const { user } = useAuthStore.getState()
  if (user?.role !== 'admin' && user?.role !== 'staff') throw redirect({ to: '/dashboard' })
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
// An administrator on an install upgraded to 1.3.0 is taken to the post-upgrade
// wizard from the page sign-in lands on, until it has been finished or skipped.
// Only the dashboard asks: a deep link to a ticket is not interrupted, and the
// wizard is not a trap, since every step can be skipped. A settings read that
// fails lets them through; the wizard is not worth locking anyone out of the app.
async function dashboardGuard() {
  await requireAuth()
  if (useAuthStore.getState().user?.role !== 'admin') return
  let settings: Record<string, unknown>
  try {
    settings = await getSettings()
  } catch {
    return
  }
  if (needsAfterUpgrade(settings)) throw redirect({ to: '/after-upgrade' })
}

const dashboardRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/dashboard',
  beforeLoad: dashboardGuard,
  component: DashboardPage,
})

// The wizard itself: administrators only, and only while it has not been
// answered, so it cannot be reopened by typing the address.
const afterUpgradeRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/after-upgrade',
  beforeLoad: async () => {
    await requireAdmin()
    if (!needsAfterUpgrade(await getSettings())) throw redirect({ to: '/dashboard' })
  },
  component: AfterUpgradePage,
})

const ticketsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/tickets',
  beforeLoad: requireAuth,
  validateSearch: (search: Record<string, unknown>) => ({
    status: typeof search.status === 'string' ? search.status : undefined,
    reporter:
      typeof search.reporter === 'string'
        ? search.reporter
        : undefined,
  }),
  component: TicketListPage,
})

const newTicketRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/tickets/new',
  beforeLoad: requireAuth,
  component: NewTicketPage,
})

const accountRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/account',
  beforeLoad: requireAuth,
  component: AccountPage,
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

// #129: staff and admin, not admin-only — the route's own guard matches the
// backend's RequireRole(admin, staff) on GET /admin/audit.
const adminAuditRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/audit',
  beforeLoad: requireStaffOrAdmin,
  component: AdminAuditPage,
})

const adminTagsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/tags',
  beforeLoad: requireAdmin,
  component: TagsPage,
})

const adminCannedResponsesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/canned-responses',
  beforeLoad: requireAdmin,
  component: CannedResponsesPage,
})

const adminCustomFieldsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/custom-fields',
  beforeLoad: requireAdmin,
  component: CustomFieldsPage,
})

const adminAPIKeysRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/api-keys',
  beforeLoad: requireAdmin,
  component: APIKeysPage,
})

const adminOAuthClientsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/oauth-clients',
  beforeLoad: requireAdmin,
  component: OAuthClientsPage,
})

const adminWebhooksRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/webhooks',
  beforeLoad: requireAdmin,
  component: WebhooksPage,
})

const adminEmailTemplatesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/email-templates',
  beforeLoad: requireAdmin,
  component: EmailTemplatesPage,
})

const adminRoutingRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/routing',
  beforeLoad: requireAdmin,
  component: RoutingPage,
})

// ── Guest ─────────────────────────────────────────────────────────────────────
const guestViewRoute = createRoute({
  getParentRoute: () => rootRoute,
  // No token in the path: it rides in the fragment, which is never sent to a
  // server and so cannot be logged by ours or by anything in front of it.
  path: '/g',
  component: GuestTicketViewPage,
})

const guestTrackRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/track',
  component: GuestTrackPage,
})

const submitRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/submit',
  component: GuestTicketPage,
})

const signupRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/signup',
  component: SignupPage,
})

const verifyEmailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/verify-email',
  component: VerifyEmailPage,
})

const forgotPasswordRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/forgot-password',
  component: ForgotPasswordPage,
})

const resetPasswordRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/reset-password',
  component: ResetPasswordPage,
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
    signupRoute,
    verifyEmailRoute,
    forgotPasswordRoute,
    resetPasswordRoute,
    dashboardRoute,
    afterUpgradeRoute,
    submitRoute,
    guestViewRoute,
    guestTrackRoute,
    ticketsRoute,
    newTicketRoute,
    ticketDetailRoute,
    accountRoute,
    adminUsersRoute,
    adminUserDetailRoute,
    adminGroupsRoute,
    adminRolesRoute,
    adminCategoriesRoute,
    adminStatusesRoute,
    adminTagsRoute,
    adminCannedResponsesRoute,
    adminCustomFieldsRoute,
    adminAPIKeysRoute,
  adminOAuthClientsRoute,
  adminWebhooksRoute,
  adminEmailTemplatesRoute,
  adminRoutingRoute,
    adminSettingsRoute,
    adminAuditRoute,
  ]),
})

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}
