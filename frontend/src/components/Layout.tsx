import { Link, useRouterState } from '@tanstack/react-router'
import { useAuthStore } from '@/store/auth'
import { logout } from '@/api/auth'
import { Button } from '@/components/ui/button'
import { TicketIcon, UsersIcon, SettingsIcon, LogOutIcon, HomeIcon, FolderIcon, CircleDotIcon } from 'lucide-react'
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

  async function handleLogout() {
    await logout().catch(() => {})
    clear()
    window.location.href = '/login'
  }

  return (
    <div className="flex h-screen bg-gray-50">
      {/* Sidebar */}
      <aside className="flex w-60 flex-col border-r bg-white">
        <div className="flex h-14 items-center border-b px-4">
          <span className="text-lg font-semibold text-gray-900">Open Help Desk</span>
        </div>

        <nav className="flex-1 space-y-1 p-3">
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
              <NavItem to="/admin/categories" icon={<FolderIcon className="h-4 w-4" />} label="Categories" />
              <NavItem to="/admin/statuses" icon={<CircleDotIcon className="h-4 w-4" />} label="Statuses" />
              <NavItem to="/admin/settings" icon={<SettingsIcon className="h-4 w-4" />} label="Settings" />
            </>
          )}
        </nav>

        <div className="border-t p-3">
          <div className="mb-2 px-3 text-xs text-gray-500 truncate">{user?.email}</div>
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
  )
}
