import { Link, useRouterState } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { useAuthStore } from '@/store/auth'
import { logout } from '@/api/auth'
import { getSiteConfig } from '@/api/admin'
import { Button } from '@/components/ui/button'
import { TicketIcon, UsersIcon, SettingsIcon, LogOutIcon, HomeIcon, FolderIcon, CircleDotIcon, ShieldIcon, UsersRoundIcon, TagIcon, SlidersIcon } from 'lucide-react'
import { cn } from '@/lib/utils'

interface NavItemProps {
  to: string
  icon: React.ReactNode
  label: string
}

function NavItem({ to, icon, label }: NavItemProps) {
  const { location } = useRouterState()
  const active = location.pathname === to || location.pathname.startsWith(to + '/')
  return (
    <Link
      to={to}
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

interface LayoutProps {
  children: React.ReactNode
}

export function Layout({ children }: LayoutProps) {
  const { user, clear } = useAuthStore()

  const { data: siteConfig } = useQuery({
    queryKey: ['site-config'],
    queryFn: getSiteConfig,
    staleTime: 5 * 60 * 1000, // refresh at most every 5 min
  })

  const siteName = siteConfig?.name ?? 'Go Help Desk'
  const logoURL = siteConfig?.logo_url ?? ''
  const version = siteConfig?.version ?? ''

  async function handleLogout() {
    await logout().catch(() => {})
    clear()
    window.location.href = '/login'
  }

  return (
    <div className="flex h-screen flex-col bg-gray-50">
      {/* Body row: sidebar + main */}
      <div className="flex flex-1 overflow-hidden">
        {/* Sidebar */}
        <aside className="flex w-60 flex-col border-r bg-white">
          {/* Branding */}
          <div className="flex h-14 items-center border-b px-4">
            {logoURL ? (
              <img src={logoURL} alt={siteName} className="h-8 max-w-[160px] object-contain" />
            ) : (
              <span className="text-lg font-semibold text-gray-900">{siteName}</span>
            )}
          </div>

          <nav className="flex-1 space-y-1 overflow-y-auto p-3">
            <NavItem to="/dashboard" icon={<HomeIcon className="h-4 w-4" />} label="Dashboard" />
            <NavItem to="/tickets" icon={<TicketIcon className="h-4 w-4" />} label="Tickets" />
            {(user?.role === 'admin' || user?.role === 'staff') && (
              <>
                <div className="px-3 pt-4 pb-1">
                  <span className="text-xs font-semibold uppercase tracking-wider text-gray-400">Admin</span>
                </div>
                <NavItem to="/admin/users" icon={<UsersIcon className="h-4 w-4" />} label="Users" />
              </>
            )}
            {user?.role === 'admin' && (
              <>
                <NavItem to="/admin/groups" icon={<UsersRoundIcon className="h-4 w-4" />} label="Groups" />
                <NavItem to="/admin/roles" icon={<ShieldIcon className="h-4 w-4" />} label="Roles" />
                <NavItem to="/admin/categories" icon={<FolderIcon className="h-4 w-4" />} label="Categories" />
                <NavItem to="/admin/statuses" icon={<CircleDotIcon className="h-4 w-4" />} label="Statuses" />
                <NavItem to="/admin/tags" icon={<TagIcon className="h-4 w-4" />} label="Tags" />
                <NavItem to="/admin/custom-fields" icon={<SlidersIcon className="h-4 w-4" />} label="Custom Fields" />
                <NavItem to="/admin/settings" icon={<SettingsIcon className="h-4 w-4" />} label="Settings" />
              </>
            )}
          </nav>

          {/* User */}
          <div className="border-t p-3 space-y-2">
            <div className="px-3 text-xs text-gray-500 truncate">{user?.email}</div>
            <Button variant="ghost" size="sm" className="w-full justify-start gap-2" onClick={handleLogout}>
              <LogOutIcon className="h-4 w-4" />
              Sign out
            </Button>
          </div>
        </aside>

        {/* Main */}
        <main className="flex-1 overflow-auto">
          <div className="mx-auto max-w-5xl p-6">{children}</div>
        </main>
      </div>

      {/* Footer — full width across the bottom */}
      {version && (
        <footer className="border-t bg-white px-6 py-2 text-center text-[11px] text-gray-400">
          Powered by{' '}
          <a
            href="https://github.com/open-help-desk/open-help-desk"
            target="_blank"
            rel="noopener noreferrer"
            className="underline decoration-dotted hover:decoration-solid"
          >
            Go Help Desk
          </a>{' '}
          v{version}
        </footer>
      )}
    </div>
  )
}
