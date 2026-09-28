import { useEffect, useState } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { Link, useRouterState } from '@tanstack/react-router'
import { useAuthStore } from '@/store/auth'
import { logout } from '@/api/auth'
import { useSiteBranding } from '@/hooks/useSiteBranding'
import { InsecureConfigBanner } from '@/components/InsecureConfigBanner'
import { BrandLogo } from '@/components/BrandLogo'
import { Button } from '@/components/ui/button'
import { TicketIcon, UsersIcon, SettingsIcon, LogOutIcon, HomeIcon, FolderIcon, CircleDotIcon, ShieldIcon, UsersRoundIcon, TagIcon, SlidersIcon, KeyIcon, MessageSquareTextIcon, PlugIcon, MenuIcon,
  UserIcon, HistoryIcon,
} from 'lucide-react'
import { cn } from '@/lib/utils'
import type { User } from '@/api/types'

interface NavItemProps {
  to: string
  icon: React.ReactNode
  label: string
  onNavigate?: () => void
}

function NavItem({ to, icon, label, onNavigate }: NavItemProps) {
  const { location } = useRouterState()
  const active = location.pathname === to || location.pathname.startsWith(to + '/')
  return (
    <Link
      to={to}
      onClick={onNavigate}
      className={cn(
        'flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium transition-colors',
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

interface SidebarContentProps {
  user: User | null
  branding: { showLogo: boolean; logoURL: string; siteName: string; onLogoError: () => void }
  onNavigate?: () => void
  onLogout: () => void
}

// The sidebar's actual content — branding, nav, sign-out — shared between the
// permanent desktop <aside> and the mobile drawer so the two never drift.
function SidebarContent({ user, branding, onNavigate, onLogout }: SidebarContentProps) {
  return (
    <>
      <div className="flex h-14 items-center border-b px-4">
        <BrandLogo
          {...branding}
          imgClassName="h-8 max-w-[160px] object-contain"
          fallbackClassName="text-lg font-semibold text-gray-900"
        />
      </div>

      <nav className="flex-1 space-y-1 overflow-y-auto p-3">
        <NavItem to="/dashboard" icon={<HomeIcon className="h-4 w-4" />} label="Dashboard" onNavigate={onNavigate} />
        <NavItem to="/tickets" icon={<TicketIcon className="h-4 w-4" />} label="Tickets" onNavigate={onNavigate} />
        {/* #129: staff see this too, scoped to their own tickets server-side
            — it is not an admin-only screen, unlike everything in the
            "Admin" section below. */}
        {(user?.role === 'admin' || user?.role === 'staff') && (
          <NavItem to="/admin/audit" icon={<HistoryIcon className="h-4 w-4" />} label="Audit Log" onNavigate={onNavigate} />
        )}
        {user?.role === 'admin' && (
          <>
            <div className="px-3 pt-4 pb-1">
              <span className="text-xs font-semibold uppercase tracking-wider text-gray-400">Admin</span>
            </div>
            <NavItem to="/admin/users" icon={<UsersIcon className="h-4 w-4" />} label="Users" onNavigate={onNavigate} />
          </>
        )}
        {user?.role === 'admin' && (
          <>
            <NavItem to="/admin/groups" icon={<UsersRoundIcon className="h-4 w-4" />} label="Groups" onNavigate={onNavigate} />
            <NavItem to="/admin/roles" icon={<ShieldIcon className="h-4 w-4" />} label="Roles" onNavigate={onNavigate} />
            <NavItem to="/admin/categories" icon={<FolderIcon className="h-4 w-4" />} label="Categories" onNavigate={onNavigate} />
            <NavItem to="/admin/statuses" icon={<CircleDotIcon className="h-4 w-4" />} label="Statuses" onNavigate={onNavigate} />
            <NavItem to="/admin/tags" icon={<TagIcon className="h-4 w-4" />} label="Tags" onNavigate={onNavigate} />
            <NavItem to="/admin/canned-responses" icon={<MessageSquareTextIcon className="h-4 w-4" />} label="Canned Responses" onNavigate={onNavigate} />
            <NavItem to="/admin/custom-fields" icon={<SlidersIcon className="h-4 w-4" />} label="Custom Fields" onNavigate={onNavigate} />
            <NavItem to="/admin/api-keys" icon={<KeyIcon className="h-4 w-4" />} label="API Keys" onNavigate={onNavigate} />
            <NavItem to="/admin/oauth-clients" icon={<PlugIcon className="h-4 w-4" />} label="OAuth Clients" onNavigate={onNavigate} />
            <NavItem to="/admin/settings" icon={<SettingsIcon className="h-4 w-4" />} label="Settings" onNavigate={onNavigate} />
          </>
        )}
      </nav>

      <div className="border-t p-3 space-y-2">
        <div className="px-3 text-xs text-gray-500 truncate">{user?.email}</div>
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
          <SidebarContent user={user} branding={branding} onLogout={handleLogout} />
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
