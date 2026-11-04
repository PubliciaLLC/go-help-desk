import { useEffect, useId, useState } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { Link, useRouterState } from '@tanstack/react-router'
import { useAuthStore } from '@/store/auth'
import { logout } from '@/api/auth'
import { useSiteBranding } from '@/hooks/useSiteBranding'
import { InsecureConfigBanner } from '@/components/InsecureConfigBanner'
import { clearDismissedNotices } from '@/lib/noticeDismissal'
import { BrandLogo } from '@/components/BrandLogo'
import { Button } from '@/components/ui/button'
import { TicketIcon, UsersIcon, SettingsIcon, LogOutIcon, HomeIcon, FolderIcon, CircleDotIcon, ShieldIcon, UsersRoundIcon, TagIcon, SlidersIcon, KeyIcon, MessageSquareTextIcon, PlugIcon, WebhookIcon, RouteIcon, MenuIcon,
  UserIcon, HistoryIcon, MailIcon, ChevronDownIcon, WrenchIcon,
} from 'lucide-react'
import { cn } from '@/lib/utils'
import type { User } from '@/api/types'

interface NavItemProps {
  to: string
  icon: React.ReactNode
  label: string
  onNavigate?: () => void
  // The links inside an Admin section: a little shorter, so a six-link section
  // and the three headings still fit a laptop screen. Back to full height for a
  // finger.
  dense?: boolean
}

function NavItem({ to, icon, label, onNavigate, dense }: NavItemProps) {
  const { location } = useRouterState()
  const active = location.pathname === to || location.pathname.startsWith(to + '/')
  return (
    <Link
      to={to}
      onClick={onNavigate}
      className={cn(
        'flex items-center gap-3 rounded-md px-3 text-sm font-medium transition-colors',
        dense ? 'py-1.5 pointer-coarse:py-2' : 'py-2',
        active
          ? 'bg-blue-50 text-blue-700'
          : 'text-gray-600 hover:bg-gray-100 hover:text-gray-900'
      )}
    >
      {icon}
      {label}
    </Link>
  )
}

// The admin pages, grouped. Order is the order on screen. Everything here is
// administrators only, Audit Log included: staff read the history on a ticket,
// not the whole log.
const ADMIN_SECTIONS = [
  {
    id: 'people', label: 'People',
    items: [
      { to: '/admin/users', label: 'Users', icon: <UsersIcon className="h-4 w-4" /> },
      { to: '/admin/groups', label: 'Groups', icon: <UsersRoundIcon className="h-4 w-4" /> },
      { to: '/admin/roles', label: 'Roles', icon: <ShieldIcon className="h-4 w-4" /> },
    ],
  },
  {
    id: 'tickets', label: 'Tickets',
    items: [
      { to: '/admin/categories', label: 'Categories', icon: <FolderIcon className="h-4 w-4" /> },
      { to: '/admin/statuses', label: 'Statuses', icon: <CircleDotIcon className="h-4 w-4" /> },
      { to: '/admin/tags', label: 'Tags', icon: <TagIcon className="h-4 w-4" /> },
      { to: '/admin/custom-fields', label: 'Custom Fields', icon: <SlidersIcon className="h-4 w-4" /> },
      { to: '/admin/canned-responses', label: 'Canned Responses', icon: <MessageSquareTextIcon className="h-4 w-4" /> },
      { to: '/admin/routing', label: 'Routing', icon: <RouteIcon className="h-4 w-4" /> },
    ],
  },
  {
    id: 'integrations', label: 'Integrations',
    items: [
      { to: '/admin/api-keys', label: 'API Keys', icon: <KeyIcon className="h-4 w-4" /> },
      { to: '/admin/oauth-clients', label: 'OAuth Clients', icon: <PlugIcon className="h-4 w-4" /> },
      { to: '/admin/webhooks', label: 'Webhooks', icon: <WebhookIcon className="h-4 w-4" /> },
      { to: '/admin/email-templates', label: 'Email Templates', icon: <MailIcon className="h-4 w-4" /> },
    ],
  },
  {
    id: 'system', label: 'System',
    items: [
      { to: '/admin/settings', label: 'Settings', icon: <SettingsIcon className="h-4 w-4" /> },
      { to: '/admin/audit', label: 'Audit Log', icon: <HistoryIcon className="h-4 w-4" /> },
    ],
  },
]

const ADMIN_NAV_KEY = 'ghd.admin-nav-open'

// The top-level "Admin" toggle's id in the remembered list, beside the ids of
// the sections.
const ADMIN_ID = 'admin'

// What the menu has open: Admin itself, and at most one section inside it.
// One at a time (an accordion) because Admin plus a long section is already
// what a 768-pixel-high laptop screen holds; two did not fit.
interface NavState {
  admin: boolean
  section: string | null
}

const NOTHING_OPEN: NavState = { admin: false, section: null }

// Storage can be blocked (private windows, site data off) or hold something
// another version wrote; neither may break the sidebar, so every access is
// guarded and anything unrecognised reads as "nothing remembered". That
// includes a list with several sections in it: earlier builds could leave
// several open and stored them all, which is not a state the menu has now, so
// it is not guessed at.
function readRemembered(): NavState {
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(ADMIN_NAV_KEY) ?? 'null')
    if (!Array.isArray(parsed)) return NOTHING_OPEN
    const sections = ADMIN_SECTIONS.map((s) => s.id).filter((id) => parsed.includes(id))
    if (sections.length > 1) return NOTHING_OPEN
    return { admin: parsed.includes(ADMIN_ID), section: sections[0] ?? null }
  } catch {
    return NOTHING_OPEN
  }
}

function writeRemembered(r: NavState) {
  try {
    const ids = [...(r.admin ? [ADMIN_ID] : []), ...(r.section ? [r.section] : [])]
    localStorage.setItem(ADMIN_NAV_KEY, JSON.stringify(ids))
  } catch {
    // Not remembering is the whole cost of a blocked store.
  }
}

function sectionOfPath(pathname: string): string | null {
  const hit = ADMIN_SECTIONS.find((s) => s.items.some((i) => pathname === i.to || pathname.startsWith(i.to + '/')))
  return hit?.id ?? null
}

interface AdminNav {
  isOpen: (id: string) => boolean
  toggle: (id: string) => void
}

// On an admin page the menu shows that page: Admin open, its section open, any
// other section closed. Anywhere else it shows what the visitor last left open
// by hand. Only by-hand choices are stored: storing what a page opened would
// leave every section you ever visited open and bring the flat list back. A
// by-hand close lasts until the next navigation, not for good.
// State lives here, above both copies of the sidebar (permanent and drawer),
// so toggling in one is seen by the other.
function useAdminNav(pathname: string): AdminNav {
  const current = sectionOfPath(pathname)
  const [remembered, setRemembered] = useState(readRemembered)
  const showing = (r: NavState): NavState => (current ? { admin: true, section: current } : r)
  const [live, setLive] = useState(() => showing(remembered))
  const [seenPath, setSeenPath] = useState(pathname)
  // Adjusting state while rendering (not in an effect) so the new page's
  // section is open in the same paint, not one frame later.
  if (seenPath !== pathname) {
    setSeenPath(pathname)
    setLive(showing(remembered))
  }
  const isOpen = (id: string) => (id === ADMIN_ID ? live.admin : live.section === id)
  function toggle(id: string) {
    let nextLive: NavState
    let nextRemembered: NavState
    if (id === ADMIN_ID) {
      nextLive = { ...live, admin: !live.admin }
      nextRemembered = { ...remembered, admin: nextLive.admin }
    } else if (live.section === id) {
      // Closing forgets the section only if it was the remembered one: a page
      // can have opened this one while another is what the visitor chose.
      nextLive = { ...live, section: null }
      nextRemembered = { ...remembered, section: remembered.section === id ? null : remembered.section }
    } else {
      nextLive = { ...live, section: id }
      nextRemembered = { ...remembered, section: id }
    }
    setLive(nextLive)
    setRemembered(nextRemembered)
    writeRemembered(nextRemembered)
  }
  return { isOpen, toggle }
}

function AdminSections({ nav, onNavigate }: { nav: AdminNav; onNavigate?: () => void }) {
  // The permanent sidebar and the open drawer are both in the document, so
  // fixed ids would collide and aria-controls would point at the wrong copy.
  const uid = useId()
  const adminOpen = nav.isOpen(ADMIN_ID)
  const adminPanelId = `${uid}-admin-panel`
  return (
    <div>
      <button
        type="button"
        aria-expanded={adminOpen}
        aria-controls={adminPanelId}
        onClick={() => nav.toggle(ADMIN_ID)}
        className="flex w-full items-center gap-3 rounded-md px-3 py-2 text-sm font-medium text-gray-600 hover:bg-gray-100 hover:text-gray-900 pointer-coarse:min-h-11"
      >
        <WrenchIcon className="h-4 w-4" />
        <span className="flex-1 text-left">Admin</span>
        <ChevronDownIcon className={cn('h-4 w-4 transition-transform', !adminOpen && '-rotate-90')} />
      </button>
      {/* Closed panels stay in the document (hidden) so aria-controls always
          resolves; hidden also takes their links out of the tab order. */}
      <div id={adminPanelId} hidden={!adminOpen} className="ml-4 border-l border-gray-200 pl-2">
        {ADMIN_SECTIONS.map((s) => {
          const open = nav.isOpen(s.id)
          const headingId = `${uid}-${s.id}-heading`
          const panelId = `${uid}-${s.id}-panel`
          return (
            <div key={s.id}>
              <button
                type="button"
                id={headingId}
                aria-expanded={open}
                aria-controls={panelId}
                onClick={() => nav.toggle(s.id)}
                className="flex w-full items-center justify-between rounded-md px-3 py-1 text-xs font-semibold uppercase tracking-wider text-gray-500 hover:bg-gray-100 hover:text-gray-900 pointer-coarse:min-h-11"
              >
                {s.label}
                <ChevronDownIcon className={cn('h-4 w-4 transition-transform', !open && '-rotate-90')} />
              </button>
              <div id={panelId} role="group" aria-labelledby={headingId} hidden={!open} className="ml-2 space-y-1">
                {s.items.map((i) => (
                  <NavItem key={i.to} to={i.to} icon={i.icon} label={i.label} onNavigate={onNavigate} dense />
                ))}
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}

interface SidebarContentProps {
  user: User | null
  adminNav: AdminNav
  branding: { showLogo: boolean; logoURL: string; siteName: string; onLogoError: () => void }
  onNavigate?: () => void
  onLogout: () => void
}

// The sidebar's actual content — branding, nav, sign-out — shared between the
// permanent desktop <aside> and the mobile drawer so the two never drift.
function SidebarContent({ user, adminNav, branding, onNavigate, onLogout }: SidebarContentProps) {
  return (
    <>
      <div className="flex h-14 items-center border-b px-4">
        <BrandLogo
          {...branding}
          imgClassName="h-8 max-w-[160px] object-contain"
          fallbackClassName="text-lg font-semibold text-gray-900 wrap-anywhere"
        />
      </div>

      <nav className="flex-1 space-y-1 overflow-y-auto p-3">
        <NavItem to="/dashboard" icon={<HomeIcon className="h-4 w-4" />} label="Dashboard" onNavigate={onNavigate} />
        <NavItem to="/tickets" icon={<TicketIcon className="h-4 w-4" />} label="Tickets" onNavigate={onNavigate} />
        {user?.role === 'admin' && <AdminSections nav={adminNav} onNavigate={onNavigate} />}
      </nav>

      <div className="border-t p-3 space-y-2">
        <div className="px-3 text-xs text-gray-500 truncate" title={user?.email}>{user?.email}</div>
        {/* Your own account, not an admin screen: it sits with the identity
            it belongs to rather than in the nav list above. */}
        <NavItem
          to="/account"
          icon={<UserIcon className="h-4 w-4" />}
          label="Your account"
          onNavigate={onNavigate}
        />
        <Button variant="ghost" size="sm" className="w-full justify-start gap-2" onClick={onLogout}>
          <LogOutIcon className="h-4 w-4" />
          Sign out
        </Button>
      </div>
    </>
  )
}

interface LayoutProps {
  children: React.ReactNode
}

export function Layout({ children }: LayoutProps) {
  const { user, clear } = useAuthStore()
  const adminNav = useAdminNav(useRouterState().location.pathname)

  const { name: siteName, logoURL, version } = useSiteBranding()

  // A stored logo_url can outlive the file it points at — a failed upload, a
  // lost volume. The header rendered the image instead of the name, so a dead
  // URL left it blank with no way to tell what instance you were looking at.
  const [logoBroken, setLogoBroken] = useState(false)
  const showLogo = logoURL !== '' && !logoBroken
  const branding = { showLogo, logoURL, siteName, onLogoError: () => setLogoBroken(true) }

  // Below md the permanent sidebar (240px of a ~390px phone) would consume
  // most of the screen, so it is replaced by this drawer instead — closed by
  // default, opened from the mobile top bar's own trigger. See #296.
  const [drawerOpen, setDrawerOpen] = useState(false)

  // Rotating a phone to landscape, or resizing past md some other way, must
  // not leave the drawer open behind the now-visible permanent sidebar: it is
  // still focus-trapped and still covers the page with its overlay, and
  // nothing on screen would explain why the app has stopped responding.
  useEffect(() => {
    const query = window.matchMedia('(min-width: 48rem)')
    function onChange(e: MediaQueryListEvent | MediaQueryList) {
      if (e.matches) setDrawerOpen(false)
    }
    onChange(query)
    query.addEventListener('change', onChange)
    return () => query.removeEventListener('change', onChange)
  }, [])

  async function handleLogout() {
    await logout().catch(() => {})
    clearDismissedNotices()
    clear()
    window.location.href = '/login'
  }

  return (
    <div className="flex h-dvh flex-col bg-gray-50">
      {/* Above everything, including the sidebar: an instance signing sessions
          with a published key is not a detail to scroll past. */}
      <InsecureConfigBanner isAdmin={user?.role === 'admin'} />

      {/* Mobile top bar: the sidebar's branding plus the drawer trigger,
          replacing the permanent sidebar below md. */}
      <div className="flex h-14 shrink-0 items-center gap-2 border-b bg-white px-3 md:hidden">
        <button
          type="button"
          aria-label="Open navigation menu"
          onClick={() => setDrawerOpen(true)}
          className="flex h-10 w-10 shrink-0 items-center justify-center rounded-md text-gray-600 hover:bg-gray-100"
        >
          <MenuIcon className="h-5 w-5" />
        </button>
        <BrandLogo
          {...branding}
          imgClassName="h-7 max-w-[140px] object-contain"
          fallbackClassName="truncate text-base font-semibold text-gray-900"
        />
      </div>

      {/* Body row: sidebar + main */}
      <div className="flex flex-1 overflow-hidden">
        {/* Sidebar — permanent from md up */}
        <aside className="hidden w-60 flex-col border-r bg-white md:flex">
          <SidebarContent user={user} adminNav={adminNav} branding={branding} onLogout={handleLogout} />
        </aside>

        {/* Sidebar — a drawer below md, over the content rather than beside
            it, since there is no room to share. */}
        <Dialog.Root open={drawerOpen} onOpenChange={setDrawerOpen}>
          <Dialog.Portal>
            <Dialog.Overlay className="fixed inset-0 z-40 bg-black/40 md:hidden" />
            <Dialog.Content
              className="fixed inset-y-0 left-0 z-50 flex w-72 max-w-[85vw] flex-col bg-white shadow-lg outline-none md:hidden"
              aria-describedby={undefined}
            >
              {/* Visually redundant with the top bar's own branding right
                  behind the overlay — present only so the drawer has an
                  accessible name, per Radix's own a11y requirement. */}
              <Dialog.Title className="sr-only">Navigation</Dialog.Title>
              <SidebarContent
                user={user}
                adminNav={adminNav}
                branding={branding}
                onLogout={handleLogout}
                onNavigate={() => setDrawerOpen(false)}
              />
            </Dialog.Content>
          </Dialog.Portal>
        </Dialog.Root>

        {/* Main */}
        <main className="flex-1 overflow-auto">
          <div className="mx-auto max-w-5xl p-4 md:p-6">{children}</div>
        </main>
      </div>

      {/* Footer — full width across the bottom */}
      {version && (
        <footer className="border-t bg-white px-6 py-2 text-center text-[11px] text-gray-400">
          Powered by{' '}
          <a
            href="https://github.com/PubliciaLLC/go-help-desk"
            target="_blank"
            rel="noopener noreferrer"
            className="inline-block min-h-6 py-1.5 underline decoration-dotted hover:decoration-solid"
          >
            Go Help Desk
          </a>{' '}
          v{version}
        </footer>
      )}
    </div>
  )
}
