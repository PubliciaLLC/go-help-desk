import { Link } from '@tanstack/react-router'
import { addressSearch, knownCells } from '@/lib/dashboard'
import { parseTicketSearch } from '@/lib/ticketSearch'
import type { QueueCount, QueueKey } from '@/api/types'
import { cn } from '@/lib/utils'

// The SLA numbers say something only when they are above zero; no other cell
// is coloured (DESIGN.md, Dashboard, Layout).
const ALERT_TONE: Partial<Record<QueueKey, string>> = {
  sla_breached: 'text-red-600',
  sla_at_risk: 'text-amber-700',
}

function countTone(key: QueueKey, count: number): string {
  const alert = ALERT_TONE[key]
  if (!alert) return 'text-gray-900'
  return count > 0 ? alert : 'text-gray-500'
}

// A row of small numbers, each a link to the list that shows exactly those
// tickets. The cells and their counts are the server's; the label and the
// address of a cell are looked up by its key (lib/dashboard.ts).
export function QueueStrip({ queues, requester }: { queues: QueueCount[]; requester: boolean }) {
  const heading = requester ? 'Your tickets' : 'Your queue'
  return (
    <section className="space-y-2">
      <h2 className="text-base font-semibold text-gray-900">{heading}</h2>
      <nav aria-label={heading}>
        <ul className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
          {knownCells(queues).map((c) => (
            <li key={c.key}>
              <Link
                to="/tickets"
                search={parseTicketSearch(addressSearch(c.href))}
                className="flex h-full flex-col rounded-lg border bg-card px-4 py-3 shadow-sm transition-shadow hover:shadow-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 pointer-coarse:min-h-11"
              >
                <span data-testid="queue-count" className={cn('text-2xl font-bold tabular-nums', countTone(c.key, c.count))}>
                  {c.count}
                </span>{' '}
                <span className="text-sm text-gray-500 wrap-anywhere">{c.label}</span>
              </Link>
            </li>
          ))}
        </ul>
      </nav>
    </section>
  )
}
