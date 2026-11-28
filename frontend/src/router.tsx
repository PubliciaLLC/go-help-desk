import { createRouter, createRoute, createRootRoute, redirect } from '@tanstack/react-router'
import { getMe } from '@/api/auth'
import { getSetupStatus } from '@/api/setup'
import { getSettings } from '@/api/admin'
import { needsAfterUpgrade } from '@/lib/afterUpgrade'
import { AfterUpgradePage } from '@/pages/AfterUpgradePage'
import { useAuthStore } from '@/store/auth'
import { RootShell } from '@/components/theme/RootShell'
import { LoginPage } from '@/pages/LoginPage'
import { SetupPage } from '@/pages/SetupPage'
import { AccountPage } from '@/pages/AccountPage'
import { DashboardPage } from '@/pages/DashboardPage'
import { ReportsPage } from '@/pages/ReportsPage'
import { TicketListPage } from '@/pages/TicketListPage'
import { NewTicketPage } from '@/pages/NewTicketPage'
import { TicketDetailPage } from '@/pages/TicketDetailPage'
import { UsersPage } from '@/pages/admin/UsersPage'
import { UserDetailPage } from '@/pages/admin/UserDetailPage'
import { GroupsPage } from '@/pages/admin/GroupsPage'
import { CategoriesPage } from '@/pages/admin/CategoriesPage'
import { StatusesPage } from '@/pages/admin/StatusesPage'
import { RolesPage } from '@/pages/admin/RolesPage'
import { GeneralSettingsPage } from '@/pages/admin/settings/GeneralSettingsPage'
import { FeaturesSettingsPage } from '@/pages/admin/settings/FeaturesSettingsPage'
import { BrandingSettingsPage } from '@/pages/admin/settings/BrandingSettingsPage'
import { EmailSettingsPage } from '@/pages/admin/settings/EmailSettingsPage'
import { EmailTemplatesSettingsPage } from '@/pages/admin/settings/EmailTemplatesSettingsPage'
import { AttachmentSettingsPage } from '@/pages/admin/settings/AttachmentSettingsPage'
import { GuestAccessSettingsPage } from '@/pages/admin/settings/GuestAccessSettingsPage'
import { AuthenticationSettingsPage } from '@/pages/admin/settings/AuthenticationSettingsPage'
import { PrivacySettingsPage } from '@/pages/admin/settings/PrivacySettingsPage'
import { TagsPage } from '@/pages/admin/TagsPage'
import { CannedResponsesPage } from '@/pages/admin/CannedResponsesPage'
import { CustomFieldsPage } from '@/pages/admin/CustomFieldsPage'
import { APIKeysPage } from '@/pages/admin/APIKeysPage'
import { AdminAuditPage } from '@/pages/admin/AdminAuditPage'
import { OAuthClientsPage } from '@/pages/admin/OAuthClientsPage'
import { WebhooksPage } from '@/pages/admin/WebhooksPage'
import { RoutingPage } from '@/pages/admin/RoutingPage'
import { GuestTicketPage } from '@/pages/GuestTicketPage'
import { GuestTicketViewPage } from '@/pages/GuestTicketViewPage'
import { GuestTrackPage } from '@/pages/GuestTrackPage'
import { SignupPage } from '@/pages/SignupPage'
import { VerifyEmailPage } from '@/pages/VerifyEmailPage'
import { ForgotPasswordPage } from '@/pages/ForgotPasswordPage'
import { ResetPasswordPage } from '@/pages/ResetPasswordPage'
import { parseTicketSearch } from '@/lib/ticketSearch'
import { kpiSearchFromRecord, kpiSearchToRecord } from '@/lib/kpi'

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

// Staff and administrators: Reports reads GET /reports/kpi, which answers a
// requester 403. The nav item is hidden for them; this is the guard.
async function requireStaff() {
  await requireAuth()
  if (useAuthStore.getState().user?.role === 'user') throw redirect({ to: '/dashboard' })
}

// ── Root ──────────────────────────────────────────────────────────────────────
// The shell adds the appearance switch to the pages that have no sidebar (#709).
const rootRoute = createRootRoute({ component: RootShell })

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

// #131: the address holds the whole state of the page (range, dates, filters),
// so a refresh or a shared link shows the same board.
const reportsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/reports',
  beforeLoad: requireStaff,
  validateSearch: (search: Record<string, unknown>) => kpiSearchToRecord(kpiSearchFromRecord(search)),
  component: ReportsPage,
})

const ticketsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/tickets',
  beforeLoad: requireAuth,
  // The ticket list keeps its whole search in the address. The old
  // status=<id> and reporter=<id> links (Dashboard, the user page) are read by
  // the same parser, as one-value lists and a requester.
  validateSearch: (search: Record<string, unknown>) => parseTicketSearch(search),
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

// System -> settings pages (#674). One flat route per page, like every other
// admin route: no layout route and no Outlet, so a page is one component.
const adminSettingsGeneralRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/settings/general',
  beforeLoad: requireAdmin,
  component: GeneralSettingsPage,
})
const adminSettingsFeaturesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/settings/features',
  beforeLoad: requireAdmin,
  component: FeaturesSettingsPage,
})
const adminSettingsBrandingRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/settings/branding',
  beforeLoad: requireAdmin,
  component: BrandingSettingsPage,
})
const adminSettingsEmailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/settings/email',
  beforeLoad: requireAdmin,
  component: EmailSettingsPage,
})
const adminSettingsEmailTemplatesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/settings/email/templates',
  beforeLoad: requireAdmin,
  component: EmailTemplatesSettingsPage,
})
const adminSettingsAttachmentRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/settings/attachments',
  beforeLoad: requireAdmin,
  component: AttachmentSettingsPage,
})
const adminSettingsGuestAccessRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/settings/guest-access',
  beforeLoad: requireAdmin,
  component: GuestAccessSettingsPage,
})
const adminSettingsAuthenticationRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/settings/authentication',
  beforeLoad: requireAdmin,
  component: AuthenticationSettingsPage,
})
const adminSettingsPrivacyRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/settings/privacy',
  beforeLoad: requireAdmin,
  component: PrivacySettingsPage,
})

// The settings used to be one page with six tabs. The tabs had no address, so
// the old address opens General, which is the tab it always opened. The
// redirect does not check the user: the page it lands on does.
const adminSettingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/settings',
  beforeLoad: () => {
    throw redirect({ to: '/admin/settings/general', replace: true })
  },
})

// #129: administrators only, as GET /admin/audit is. Staff read the history on
// a ticket, not the whole log.
const adminAuditRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/audit',
  beforeLoad: requireAdmin,
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
  // ?field=<id> opens that field's row; the Categories page links here.
  validateSearch: (search: Record<string, unknown>): { field?: string } =>
    typeof search.field === 'string' && search.field ? { field: search.field } : {},
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

// Email Templates is now the Templates tab of System -> Email (#674). The
// query is kept, so a link to a template (?t=) still opens it.
const adminEmailTemplatesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin/email-templates',
  beforeLoad: ({ search }) => {
    throw redirect({ to: '/admin/settings/email/templates', search, replace: true })
  },
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
    reportsRoute,
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
    adminSettingsGeneralRoute,
    adminSettingsFeaturesRoute,
    adminSettingsBrandingRoute,
    adminSettingsEmailRoute,
    adminSettingsEmailTemplatesRoute,
    adminSettingsAttachmentRoute,
    adminSettingsGuestAccessRoute,
    adminSettingsAuthenticationRoute,
    adminSettingsPrivacyRoute,
    adminAuditRoute,
  ]),
})

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}
