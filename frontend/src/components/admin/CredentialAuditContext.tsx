// Says which OAuth client or webhook an audit entry is about. AuditDiff renders
// nothing for a create (no "before") or a delete (no "after"), and lists only the
// keys that changed for an update, so without this line a row would not name the
// client or hook it belongs to.
//
// client_name is text an administrator typed; url_host is only the host of a
// webhook's URL, because the rest of it holds the token (the server never
// writes it). Both are allow-listed by the server (audit.shownKeys) and shown
// as text, never as markup.

function text(side: Record<string, unknown> | null | undefined, key: string): string {
  const v = side?.[key]
  return typeof v === 'string' ? v : ''
}

export function CredentialAuditContext({
  entityType,
  before,
  after,
}: {
  entityType: string
  before?: Record<string, unknown> | null
  after?: Record<string, unknown> | null
}) {
  // After is the newer side; a delete has only before.
  const pick = (key: string) => text(after, key) || text(before, key)
  const parts =
    entityType === 'oauth_client'
      ? [pick('client_name'), pick('scopes')]
      : [pick('url_host'), pick('events')]
  const line = parts.filter(Boolean).join(' · ')
  if (!line) return null
  return <p className="mt-0.5 text-gray-600 wrap-anywhere">{line}</p>
}
