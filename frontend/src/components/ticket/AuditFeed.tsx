import { useQuery } from '@tanstack/react-query'
import { listTicketAudit } from '@/api/tickets'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { AuditDiff } from '@/components/AuditDiff'

interface AuditFeedProps {
  ticketId: string
}

// The action strings ticket.Service writes today. Not exhaustive on
// purpose: an action this map doesn't know falls back to a readable guess
// from the raw string rather than a blank line, so a new action added on the
// backend later shows up as something instead of nothing.
const ACTION_LABELS: Record<string, string> = {
  created: 'Ticket filed',
  status_changed: 'Status changed',
  assigned: 'Assigned',
  unassigned: 'Unassigned',
  resolved: 'Resolved',
  closed: 'Closed',
  reopened: 'Reopened',
}

function labelFor(action: string): string {
  return (
    ACTION_LABELS[action] ??
    action.replace(/_/g, ' ').replace(/^./, (c) => c.toUpperCase())
  )
}

// #129: the per-ticket activity feed, next to the status timeline —
// answers "who changed this and when" without database access. It inherits
// the ticket's own visibility gate; the one action-specific exception
// (assignment is staff/admin-only, and reporters can't resolve an assignee
// to a name anywhere else) is enforced server-side — the API simply omits
// actor_name/actor_id on those entries for a reporting user, so there is
// nothing this component needs to filter itself.
export function AuditFeed({ ticketId }: AuditFeedProps) {
  const { data: entries = [], isLoading, isError } = useQuery({
    queryKey: ['ticketAudit', ticketId],
    queryFn: () => listTicketAudit(ticketId),
  })

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-xs font-semibold uppercase tracking-wider text-gray-400">
          Activity
        </CardTitle>
      </CardHeader>
      <CardContent>
        {isLoading && <p className="text-xs text-gray-400">Loading…</p>}
        {isError && <p className="text-xs text-red-600">Could not load activity</p>}
        {!isLoading && !isError && entries.length === 0 && (
          <p className="text-xs text-gray-400">Nothing recorded yet</p>
        )}
        <ul className="space-y-2">
          {entries.map((e) => (
            <li key={e.id} className="text-xs text-gray-700">
              <span className="font-medium">{labelFor(e.action)}</span>
              {e.actor_name && (
                <>
                  {' by '}
                  {e.actor_masked
                    ? <span className="italic" title="Hidden by the Privacy setting">Requester (name hidden)</span>
                    : <span className="font-medium">{e.actor_name}</span>}
                </>
              )}
              <span className="block text-gray-400">
                {new Date(e.created_at).toLocaleString()}
              </span>
              <AuditDiff before={e.before} after={e.after} />
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  )
}
