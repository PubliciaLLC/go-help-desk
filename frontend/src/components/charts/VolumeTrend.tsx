import { AreaChart } from './AreaChart'
import type { DashboardVolume } from '@/api/types'

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

// "2026-10-26" is a calendar day in the viewer's zone, so it is read as the
// parts it is made of: building a Date from the string would be UTC midnight,
// which is the day before in a zone west of Greenwich.
function shortDate(ymd: string): string {
  const [, m, d] = ymd.split('-').map(Number)
  return `${d} ${MONTHS[m - 1]}`
}

export interface VolumeTrendProps {
  volume: DashboardVolume
  // The dashboard's wording is the default; the Reports page says its own period.
  ariaLabel?: string
  emptyText?: string
}

// Tickets created and resolved per day. The chart is the shared module's; this
// only says what is drawn. Used by the dashboard (last 30 days) and by Reports.
export function VolumeTrend({
  volume,
  ariaLabel = 'Tickets created and resolved per day, last 30 days',
  emptyText = 'No tickets were created or resolved in the last 30 days.',
}: VolumeTrendProps) {
  return (
    <AreaChart
      labels={volume.days.map((d) => d.date)}
      series={[
        { key: 'created', label: 'Created', values: volume.days.map((d) => d.created), tone: 'blue' },
        { key: 'resolved', label: 'Resolved', values: volume.days.map((d) => d.resolved), tone: 'green' },
      ]}
      ariaLabel={ariaLabel}
      formatLabel={shortDate}
      emptyText={emptyText}
    />
  )
}
