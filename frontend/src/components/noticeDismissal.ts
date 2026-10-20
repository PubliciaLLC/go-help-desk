export const DISMISS_PREFIX = 'ghd.notice-dismissed.'

// Read once per mount; sessionStorage is per tab and gone on sign-out/close,
// which is exactly the lifetime a dismissal should have.
export function readDismissed(): Set<string> {
  const ids = new Set<string>()
  try {
    for (let i = 0; i < sessionStorage.length; i++) {
      const k = sessionStorage.key(i)
      if (k?.startsWith(DISMISS_PREFIX)) ids.add(k.slice(DISMISS_PREFIX.length))
    }
  } catch {
    // Storage blocked: nothing is dismissed.
  }
  return ids
}

/**
 * A tab outlives a sign-out (the login page is a navigation in the same tab),
 * so without this a hidden notice would stay hidden through the next sign-in.
 */
export function clearDismissedNotices() {
  try {
    for (const id of Array.from(readDismissed())) sessionStorage.removeItem(DISMISS_PREFIX + id)
  } catch {
    // Storage blocked: nothing was stored.
  }
}
