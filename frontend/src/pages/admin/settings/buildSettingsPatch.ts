import { shownAttachmentMaxMB } from '@/lib/attachmentLimit'

/** The body of a PATCH /admin/settings for one save (#674).
 *
 *  stored   the settings dump as last fetched, or undefined before it arrives
 *  local    the draft: every key the page holds, as the dump shaped it
 *  secrets  write-only values typed in this session, by settings key
 *  cleared  write-only keys the operator asked to remove, by settings key
 *
 *  Only what changed is sent. Keys the dump calls "<key>_set" describe a
 *  secret the server will not return, so they are never sent back. */
export function buildSettingsPatch(
  stored: Record<string, unknown> | undefined,
  local: Record<string, unknown>,
  secrets: Record<string, string>,
  cleared: Record<string, boolean>,
): Record<string, unknown> {
  const patch: Record<string, unknown> = {}
  // The per-file limit the page shows (#673): a guest figure above it is sent
  // held to it, because the server refuses the figure whole otherwise.
  const overallMB = shownAttachmentMaxMB(local.attachment_max_mb)
  for (const [k, v] of Object.entries(local)) {
    if (k.endsWith('_set')) continue
    // JSON.stringify so an untouched array compares equal; a key absent from
    // the dump stringifies to undefined and is always sent.
    if (stored && JSON.stringify(stored[k]) === JSON.stringify(v)) continue
    patch[k] = k === 'guest_attachment_max_mb' && typeof v === 'number' ? Math.min(v, overallMB) : v
  }
  const keys = new Set([...Object.keys(secrets), ...Object.keys(cleared)])
  for (const k of keys) {
    if (cleared[k]) {
      patch[k] = ''
      continue
    }
    const typed = (secrets[k] ?? '').trim()
    if (typed !== '') patch[k] = typed
  }
  return patch
}
