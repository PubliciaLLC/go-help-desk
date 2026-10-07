// Shared by AuditFeed (per-ticket) and AdminAuditPage (#129's admin-wide
// view) — both show the same before/after shape, gated by the same server
// rule (see TicketAuditEntry / AdminAuditEntry's own comments), so the
// rendering lives once rather than drifting between two copies.

function formatValue(v: unknown): string {
  if (v === null || v === undefined) return '—'
  return typeof v === 'string' ? v : JSON.stringify(v)
}

// Must match audit.redactedPlaceholder on the server.
const REDACTED = '[redacted]'

function containsRedacted(v: unknown): boolean {
  if (v === REDACTED) return true
  if (Array.isArray(v)) return v.some(containsRedacted)
  if (v !== null && typeof v === 'object') return Object.values(v).some(containsRedacted)
  return false
}

// The server swaps a secret for the same placeholder on both sides, so a
// secret that changed compares equal to itself. The diff cannot tell a
// rotated secret from an untouched one, and hiding both would make a rotation
// invisible to the admin it is meant to inform; showing both is the honest
// side to err on. (#329)
function isChanged(b: unknown, a: unknown): boolean {
  return JSON.stringify(b) !== JSON.stringify(a) || (containsRedacted(b) && containsRedacted(a))
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
  const changed = keys.filter((k) => isChanged(before[k], after[k]))
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
