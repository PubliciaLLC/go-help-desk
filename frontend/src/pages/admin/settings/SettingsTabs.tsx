import { Link, useRouterState } from '@tanstack/react-router'
import { cn } from '@/lib/utils'

export interface SettingsTab {
  label: string
  to: string
}

/**
 * The tab bar of a settings page that has more than one address (#674). The
 * tabs are links, not role="tab" buttons: each tab has its own address, Back
 * works between them and a notice can link straight to one. A tab is current
 * when the pathname is exactly its address.
 */
export function SettingsTabs({ page, tabs }: { page: string; tabs: SettingsTab[] }) {
  const { location } = useRouterState()
  return (
    <div className="border-b">
      <nav aria-label={`${page} sections`} className="-mb-px flex gap-6 overflow-x-auto">
        {tabs.map((tab) => {
          const current = location.pathname === tab.to
          return (
            <Link
              key={tab.to}
              to={tab.to}
              // The router's own active rule is a prefix match, which would mark
              // Notifications current on Templates; and ?t= is not part of the tab.
              activeOptions={{ exact: true, includeSearch: false }}
              aria-current={current ? 'page' : undefined}
              className={cn(
                'border-b-2 pb-3 text-sm font-medium whitespace-nowrap transition-colors',
                current
                  ? 'border-ring text-selected-fg'
                  : 'border-transparent text-fg-faint hover:border-border-strong hover:text-fg-soft'
              )}
            >
              {tab.label}
            </Link>
          )
        })}
      </nav>
    </div>
  )
}
