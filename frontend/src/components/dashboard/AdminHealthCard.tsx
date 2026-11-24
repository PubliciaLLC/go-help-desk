import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { getSettings, securityWarningsQuery } from '@/api/admin'
import { Button } from '@/components/ui/button'
import { adminHealthItems, type HealthItem } from '@/lib/adminHealth'

// Administrators only (the dashboard decides). It reads the two queries an
// administrator's page already makes, under the same keys and options as the
// banner and the settings pages, so it adds no request. It has no Hide: it says
// what is true now, and the banner's Hide governs only the banner.

const TONE: Record<HealthItem['severity'], string> = {
  info: 'border-blue-200 bg-blue-50 text-blue-900',
  amber: 'border-amber-200 bg-amber-50 text-amber-900',
  red: 'border-red-200 bg-red-50 text-red-900',
}

export function AdminHealthCard() {
  const warnings = useQuery(securityWarningsQuery)
  const settings = useQuery({ queryKey: ['admin', 'settings'], queryFn: getSettings })

  if (warnings.isError || settings.isError) {
    return (
      <div role="alert" className="flex flex-wrap items-center gap-3 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-800">
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
    return <p className="text-sm text-gray-500">No problems to report.</p>
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
