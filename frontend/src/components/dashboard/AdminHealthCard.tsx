import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { getSettings, securityWarningsQuery } from '@/api/admin'
import { Button } from '@/components/ui/button'
import { adminHealthItems, type HealthItem } from '@/lib/adminHealth'

// Administrators only (the dashboard decides). It reads the banner's
// security-warnings query and the settings dump, under the same keys and
// options as the banner and the settings pages; on the dashboard the settings
// dump is one extra GET for an administrator. It has no Hide: it says
// what is true now, and the banner's Hide governs only the banner.

const TONE: Record<HealthItem['severity'], string> = {
  info: 'border-info-border bg-info-soft text-info-fg-strong',
  amber: 'border-warning-border bg-warning-soft text-warning-fg-strong',
  red: 'border-danger-border bg-danger-soft text-danger-fg-strong',
}

export function AdminHealthCard() {
  const warnings = useQuery(securityWarningsQuery)
  const settings = useQuery({ queryKey: ['admin', 'settings'], queryFn: getSettings })

  if (warnings.isError || settings.isError) {
    return (
      <div role="alert" className="flex flex-wrap items-center gap-3 rounded-lg border border-danger-border bg-danger-soft px-4 py-3 text-sm text-danger-fg-strong">
        <span>Health checks could not be loaded.</span>
        <Button
          variant="outline"
          size="sm"
          disabled={warnings.isFetching || settings.isFetching}
          onClick={() => {
            void warnings.refetch()
            void settings.refetch()
          }}
        >
          Retry
        </Button>
      </div>
    )
  }

  // Nothing until both have answered: "No problems" must mean checked.
  if (!warnings.data || !settings.data) return null

  const items = adminHealthItems(warnings.data, settings.data)
  if (items.length === 0) {
    return <p className="text-sm text-fg-faint">No problems to report.</p>
  }

  return (
    <section aria-label="Health" className="space-y-2">
      <ul className="space-y-2">
        {items.map((i) => (
          <li key={i.id} className={`flex flex-wrap items-center gap-x-3 gap-y-1 rounded-lg border px-4 py-3 text-sm ${TONE[i.severity]}`}>
            <span className="min-w-0 wrap-anywhere">{i.text}</span>
            {i.external ? (
              <a className="font-medium underline" href={i.href} target="_blank" rel="noopener noreferrer">
                {i.place}
              </a>
            ) : (
              // Every internal address is a route (the settings pages, #674).
              <Link className="font-medium underline" to={i.href as '/'}>
                {i.place}
              </Link>
            )}
          </li>
        ))}
      </ul>
    </section>
  )
}
