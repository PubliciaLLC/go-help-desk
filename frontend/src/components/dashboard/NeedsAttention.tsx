import { Link } from '@tanstack/react-router'
import { ReopenedBadge } from '@/components/ticket/ReopenedBadge'
import { fmtAgo } from '@/lib/format'
import type { AttentionItem, AttentionReason, Status } from '@/api/types'

const REASON: Record<Exclude<AttentionReason, 'reopened'>, { label: string; className: string }> = {
  breached: { label: 'Breached', className: 'border-red-300 bg-red-50 text-red-800' },
  at_risk: { label: 'At risk', className: 'border-amber-300 bg-amber-50 text-amber-800' },
  unassigned: { label: 'Unassigned', className: 'border-gray-300 bg-gray-100 text-gray-700' },
}

function ReasonBadge({ item, statuses }: { item: AttentionItem; statuses?: Status[] }) {
  if (item.reason === 'reopened') {
    // The list holds open tickets only, and the server chose this row for its
    // reopen, so the ticket is reopened and not resolved again. The badge decides
    // that from the status name, which is unknown until the statuses load: do not
    // let that hide a badge the row exists for.
    const status = statuses?.find((s) => s.id === item.ticket.status_id)
    return <ReopenedBadge reopenCount={item.ticket.reopen_count || 1} statusName={status?.name ?? 'Open'} />
  }
  const r = REASON[item.reason]
  if (!r) return null
  return (
    <span className={`inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium ${r.className}`}>
      {r.label}
    </span>
  )
}

// Up to five tickets that want someone, each a link to the ticket. A list of
// rows and never a table, so it is the same at every width; the subject is
// ticket content and wraps.
export function NeedsAttention({ items, statuses }: { items: AttentionItem[]; statuses?: Status[] }) {
  if (items.length === 0) {
    return <p className="text-sm text-gray-500">Nothing needs attention right now.</p>
  }
  return (
    <ul className="divide-y">
      {items.map((item) => (
        <li key={item.ticket.id}>
          <Link
            to="/tickets/$id"
            params={{ id: item.ticket.id }}
            className="block space-y-1 py-2 hover:bg-gray-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 pointer-coarse:min-h-11"
          >
            <span className="flex flex-wrap items-center gap-2">
              <span className="font-mono text-xs text-gray-500 wrap-anywhere">{item.ticket.tracking_number}</span>
              <ReasonBadge item={item} statuses={statuses} />
            </span>
            <span className="block text-sm font-medium text-gray-900 wrap-anywhere">{item.ticket.subject}</span>
            <span className="block text-xs text-gray-500" title={new Date(item.since).toLocaleString()}>
              {`${item.reason === 'reopened' ? 'reopened' : 'opened'} ${fmtAgo(item.since)}`}
            </span>
          </Link>
        </li>
      ))}
    </ul>
  )
}
