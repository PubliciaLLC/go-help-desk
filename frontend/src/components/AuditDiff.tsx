// Shared by AuditFeed (per-ticket) and AdminAuditPage (#129's admin-wide
// view) — both show the same before/after shape, gated by the same server
// rule (see TicketAuditEntry / AdminAuditEntry's own comments), so the
// rendering lives once rather than drifting between two copies.

function formatValue(v: unknown): string {
  if (v === null || v === undefined) return '—'
  return typeof v === 'string' ? v : JSON.stringify(v)
}

interface AuditDiffProps {
  before?: Record<string, unknown> | null
  after?: Record<string, unknown> | null
}

// Only rendered when both sides are present: a create action's before is
// null, and "subject: — → Printer broken" reads as a change when it is
// really the initial value, so that case is left to the plain action label
// instead.
export function AuditDiff({ before, after }: AuditDiffProps) {
  if (!before || !after) return null

  const keys = Array.from(new Set([...Object.keys(before), ...Object.keys(after)]))
  const changed = keys.filter((k) => JSON.stringify(before[k]) !== JSON.stringify(after[k]))
  if (changed.length === 0) return null

  return (
    <ul className="mt-1 space-y-0.5 border-l-2 border-gray-100 pl-2">
      {changed.map((k) => (
        <li key={k} className="text-gray-500">
          <span className="font-mono">{k}</span>
          {': '}
          {formatValue(before[k])} → {formatValue(after[k])}
        </li>
      ))}
    </ul>
  )
}
