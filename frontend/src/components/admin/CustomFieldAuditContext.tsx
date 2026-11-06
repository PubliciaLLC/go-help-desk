// Says which custom field an audit entry is about. Every custom_field entry
// carries field_name on both sides, but AuditDiff lists only keys that changed
// and renders nothing when one side is missing, so a create, a delete or a
// retire would otherwise name no field at all.
//
// field_name, option_value and scope_path are text an administrator wrote, never
// ticket content; the server allow-lists them (audit.shownKeys). They are shown
// as text, never as markup.

function text(side: Record<string, unknown> | null | undefined, key: string): string {
  const v = side?.[key]
  return typeof v === 'string' ? v : ''
}

export function CustomFieldAuditContext({
  before,
  after,
}: {
  before?: Record<string, unknown> | null
  after?: Record<string, unknown> | null
}) {
  // After is the newer side; a delete has only before.
  const pick = (key: string) => text(after, key) || text(before, key)
  const name = pick('field_name')
  if (!name) return null
  const option = pick('option_value')
  const path = pick('scope_path')
  return (
    <p className="mt-0.5 text-gray-600 wrap-anywhere">
      <span className="font-medium">{name}</span>
      {option && ` · option "${option}"`}
      {path && ` · ${path}`}
    </p>
  )
}
