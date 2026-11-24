import { AreaChart } from '@/components/charts'
import type { DashboardVolume } from '@/api/types'

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

// "2026-10-26" is a calendar day in the viewer's zone, so it is read as the
// parts it is made of: building a Date from the string would be UTC midnight,
// which is the day before in a zone west of Greenwich.
function shortDate(ymd: string): string {
  const [, m, d] = ymd.split('-').map(Number)
  return `${d} ${MONTHS[m - 1]}`
}

// Tickets created and resolved per day over the last 30 days. The chart is the
// shared module's; this only says what is drawn.
export function VolumeTrend({ volume }: { volume: DashboardVolume }) {
  return (
    <AreaChart
      labels={volume.days.map((d) => d.date)}
      series={[
        { key: 'created', label: 'Created', values: volume.days.map((d) => d.created), tone: 'blue' },
        { key: 'resolved', label: 'Resolved', values: volume.days.map((d) => d.resolved), tone: 'green' },
      ]}
      ariaLabel="Tickets created and resolved per day, last 30 days"
      formatLabel={shortDate}
      emptyText="No tickets were created or resolved in the last 30 days."
    />
  )
}
